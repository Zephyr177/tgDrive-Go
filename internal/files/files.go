// Package files implements the upload/download pipeline over the Telegram
// storage backend, including the upstream chunking scheme for large files.
package files

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"net/http"
	"path"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Zephyr177/tgDrive-Go/internal/model"
	"github.com/Zephyr177/tgDrive-Go/internal/store"
	"github.com/Zephyr177/tgDrive-Go/internal/telegram"
)

// ChunkSize is the per-object Telegram upload size used upstream (10 MiB).
const ChunkSize = 10 << 20

// ErrEmptyFile mirrors the upstream "上传的文件为空！" validation.
var ErrEmptyFile = errors.New("上传的文件为空！")

// Reporter receives upload progress events (implemented by the WS hub).
type Reporter interface {
	UploadProgress(name string, percentage float64, currentChunk, totalChunks int)
	UploadComplete(name string)
	UploadError(name string, err string)
}

// Service stores and retrieves files through a telegram.Storage backend.
type Service struct {
	store    *store.Store
	backend  telegram.Storage
	reporter Reporter
}

// New returns a files service.
func New(st *store.Store, backend telegram.Storage, reporter Reporter) *Service {
	return &Service{store: st, backend: backend, reporter: reporter}
}

// recordFile is the JSON record stored on Telegram for chunked uploads.
type recordFile struct {
	FileName string   `json:"fileName"`
	FileSize int64    `json:"fileSize"`
	FileIDs  []string `json:"fileIds"`
	IsRecord bool     `json:"recordFile"`
}

// Upload stores a file (chunking when larger than ChunkSize) and inserts its
// metadata row. urlPrefix is the public prefix used to build download URLs,
// e.g. "http://host:8085"; userID may be nil (WebDAV uploads).
func (s *Service) Upload(ctx context.Context, name string, r io.Reader, size int64, userID *int64, urlPrefix string) (*model.File, error) {
	if size < 0 {
		return nil, errors.New("无法获取文件大小")
	}
	if size == 0 {
		return nil, ErrEmptyFile
	}
	var (
		fileID string
		err    error
	)
	if size <= ChunkSize {
		if s.reporter != nil {
			s.reporter.UploadProgress(name, 0, 0, 1)
		}
		uploadName := name
		// Telegram converts GIF animations to MP4; stripping the extension for
		// small uploads keeps them as documents (upstream behavior).
		if strings.HasSuffix(strings.ToLower(name), ".gif") {
			uploadName = name[:len(name)-len(".gif")]
		}
		fileID, err = s.backend.Upload(ctx, uploadName, r, size)
		if err == nil && s.reporter != nil {
			s.reporter.UploadProgress(name, 100, 1, 1)
			s.reporter.UploadComplete(name)
		}
	} else {
		fileID, err = s.uploadChunked(ctx, name, r, size)
	}
	if err != nil {
		if s.reporter != nil {
			s.reporter.UploadError(name, err.Error())
		}
		return nil, fmt.Errorf("上传文件失败！%w", err)
	}

	f := &model.File{
		FileName:    name,
		DownloadURL: urlPrefix + "/d/" + fileID,
		UploadTime:  time.Now().Unix(),
		Size:        HumanSize(size),
		FullSize:    size,
		FileID:      fileID,
		UserID:      userID,
	}
	id, err := s.store.InsertFile(f)
	if err != nil {
		return nil, err
	}
	f.ID = id
	return f, nil
}

// uploadChunked splits the stream into ChunkSize parts uploaded with bounded
// concurrency, then stores a JSON record file whose Telegram id is returned.
func (s *Service) uploadChunked(ctx context.Context, name string, r io.Reader, size int64) (string, error) {
	total := int((size + ChunkSize - 1) / ChunkSize)
	ids := make([]string, total)

	sem := make(chan struct{}, 5) // upstream PERMITS = 5
	errCh := make(chan error, total)
	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		completed int
		failed    atomic.Bool
	)
	var readErr error
	for i := range total {
		if failed.Load() {
			break
		}
		partLen := ChunkSize
		if remaining := size - int64(i)*ChunkSize; remaining < int64(partLen) {
			partLen = int(remaining)
		}
		buf := make([]byte, partLen)
		if _, err := io.ReadFull(r, buf); err != nil {
			readErr = err
			failed.Store(true)
			break
		}
		sem <- struct{}{}
		wg.Add(1)
		go func(index int, data []byte) {
			defer wg.Done()
			defer func() { <-sem }()
			partName := fmt.Sprintf("%s_part%d", name, index)
			id, err := s.backend.Upload(ctx, partName, bytes.NewReader(data), int64(len(data)))
			if err != nil {
				failed.Store(true)
				errCh <- fmt.Errorf("part %d: %w", index, err)
				return
			}
			ids[index] = id
			mu.Lock()
			completed++
			cur := completed
			mu.Unlock()
			if s.reporter != nil {
				s.reporter.UploadProgress(name, float64(cur)/float64(total)*100, cur, total)
			}
		}(i, buf)
	}
	wg.Wait()
	close(errCh)
	if readErr != nil {
		return "", readErr
	}
	for err := range errCh {
		if err != nil {
			return "", err
		}
	}
	for i, id := range ids {
		if id == "" {
			return "", fmt.Errorf("part %d missing", i)
		}
	}

	rec := recordFile{FileName: name, FileSize: size, FileIDs: ids, IsRecord: true}
	data, err := json.Marshal(rec)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(name))
	recordName := hex.EncodeToString(sum[:]) + ".record.json"
	fileID, err := s.backend.Upload(ctx, recordName, bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", err
	}
	if s.reporter != nil {
		s.reporter.UploadComplete(name)
	}
	return fileID, nil
}

// Download streams a stored file, transparently reassembling chunked uploads.
// dbRow may be nil; it provides name/size fallbacks. The returned name is the
// best-known file name (extension may still be missing).
func (s *Service) Download(ctx context.Context, fileID string, dbRow *model.File) (io.ReadCloser, string, int64, error) {
	blob, err := s.backend.Open(ctx, fileID)
	if err != nil {
		return nil, "", 0, err
	}
	peek := make([]byte, 64<<10)
	n, err := io.ReadFull(blob.Reader, peek)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		blob.Reader.Close()
		return nil, "", 0, err
	}
	peek = peek[:n]

	var rec recordFile
	if json.Unmarshal(peek, &rec) == nil && rec.IsRecord && len(rec.FileIDs) > 0 {
		return s.mergeStream(ctx, rec.FileIDs), rec.FileName, rec.FileSize, nil
	}

	name := blob.Name
	size := blob.Size
	if dbRow != nil {
		if dbRow.FileName != "" {
			name = dbRow.FileName
		}
		if dbRow.FullSize > 0 {
			size = dbRow.FullSize
		}
	}
	stream := io.MultiReader(bytes.NewReader(peek), blob.Reader)
	return &combinedReadCloser{Reader: stream, closer: blob.Reader}, name, size, nil
}

// combinedReadCloser closes the underlying blob after the multi-reader drains.
type combinedReadCloser struct {
	io.Reader
	closer io.Closer
}

func (c *combinedReadCloser) Close() error { return c.closer.Close() }

// mergeStream concatenates the parts of a chunked upload.
func (s *Service) mergeStream(ctx context.Context, ids []string) io.ReadCloser {
	pr, pw := io.Pipe()
	go func() {
		for _, id := range ids {
			part, err := s.backend.Open(ctx, id)
			if err != nil {
				pw.CloseWithError(err)
				return
			}
			if _, err := io.Copy(pw, part.Reader); err != nil {
				part.Reader.Close()
				pw.CloseWithError(err)
				return
			}
			part.Reader.Close()
		}
		pw.Close()
	}()
	return pr
}

// HumanSize formats a byte count the same way upstream UserFriendly does.
func HumanSize(size int64) string {
	if size <= 0 {
		return "0 B"
	}
	units := []string{"B", "KB", "MB", "GB", "TB"}
	idx := int(math.Log10(float64(size)) / math.Log10(1024))
	if idx > len(units)-1 {
		idx = len(units) - 1
	}
	readable := float64(size) / math.Pow(1024, float64(idx))
	return fmt.Sprintf("%.1f %s", readable, units[idx])
}

// ContentTypeFor returns the response content type for a file name using the
// upstream fallback set, then the system MIME table.
func ContentTypeFor(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".gif":
		return "image/gif"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".bmp":
		return "image/bmp"
	case ".txt":
		return "text/plain; charset=utf-8"
	case ".pdf":
		return "application/pdf"
	case ".mp4":
		return "video/mp4"
	}
	if ct := mime.TypeByExtension(path.Ext(name)); ct != "" {
		return ct
	}
	return "application/octet-stream"
}

// sniffExtensions maps detected MIME types to extensions for names without one.
var sniffExtensions = map[string]string{
	"image/jpeg":      ".jpg",
	"image/png":       ".png",
	"image/gif":       ".gif",
	"image/bmp":       ".bmp",
	"video/mp4":       ".mp4",
	"video/webm":      ".webm",
	"audio/mpeg":      ".mp3",
	"text/plain":      ".txt",
	"application/pdf": ".pdf",
	"application/zip": ".zip",
}

// AppendExtension adds a sniffed extension when the base name has none,
// approximating the upstream Apache Tika behavior.
func AppendExtension(name string, peek []byte) string {
	if strings.Contains(path.Base(name), ".") {
		return name
	}
	ct := http.DetectContentType(peek)
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = strings.TrimSpace(ct[:i])
	}
	ext := sniffExtensions[ct]
	if ext == "" {
		if exts, _ := mime.ExtensionsByType(ct); len(exts) > 0 {
			ext = exts[0]
		}
	}
	return name + ext
}
