package store

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/Zephyr177/tgDrive-Go/internal/model"
)

const fileColumns = `f.id, f.file_name, COALESCE(f.download_url, ''), f.upload_time, COALESCE(f.size, ''), COALESCE(f.full_size, 0), COALESCE(f.file_id, ''), COALESCE(f.webdav_path, ''), COALESCE(f.dir, 0), f.user_id, COALESCE(f.is_public, 0), f.messageID, COALESCE(u.username, '')`

func scanFile(row interface{ Scan(...any) error }) (*model.File, error) {
	var f model.File
	var userID, messageID sql.NullInt64
	if err := row.Scan(&f.ID, &f.FileName, &f.DownloadURL, &f.UploadTime, &f.Size,
		&f.FullSize, &f.FileID, &f.WebDAVPath, &f.Dir, &userID, &f.IsPublic, &messageID, &f.Uploader); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, sql.ErrNoRows
		}
		return nil, fmt.Errorf("scan file: %w", err)
	}
	if userID.Valid {
		f.UserID = &userID.Int64
	}
	if messageID.Valid {
		f.MessageID = &messageID.Int64
	}
	return &f, nil
}

func (s *Store) scanFiles(rows *sql.Rows) ([]model.File, error) {
	defer rows.Close()
	var out []model.File
	for rows.Next() {
		f, err := scanFile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *f)
	}
	return out, rows.Err()
}

// InsertFile inserts a file row and returns its id.
func (s *Store) InsertFile(f *model.File) (int64, error) {
	res, err := s.db.Exec(
		`INSERT INTO files (file_name, download_url, upload_time, size, full_size, file_id, webdav_path, dir, is_public, user_id, messageID)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		f.FileName, f.DownloadURL, f.UploadTime, f.Size, f.FullSize, f.FileID,
		f.WebDAVPath, f.Dir, f.IsPublic, f.UserID, f.MessageID)
	if err != nil {
		return 0, fmt.Errorf("insert file: %w", err)
	}
	return res.LastInsertId()
}

// FileByID returns the file with the given id (sql.ErrNoRows if none).
func (s *Store) FileByID(id int64) (*model.File, error) {
	return scanFile(s.db.QueryRow(
		`SELECT `+fileColumns+` FROM files f LEFT JOIN users u ON f.user_id = u.id WHERE f.id = ?`, id))
}

// FileFilter drives the paged /api/file-list query.
type FileFilter struct {
	Role         string // acting role: "admin" | "user" | other/empty (anonymous)
	UserID       *int64 // acting user id (nil for anonymous)
	FilterUserID *int64 // admin-only optional uploader filter
	Keyword      string
	Page         int
	Size         int
}

// ListFilesPaged returns total + one page of files with the upstream role logic:
// admin sees everything (optionally one uploader), user sees own + public,
// visitor/anonymous see public only.
func (s *Store) ListFilesPaged(f FileFilter) (int64, []model.File, error) {
	if f.Page < 1 {
		f.Page = 1
	}
	if f.Size < 1 {
		f.Size = 10
	}
	where := "1=1"
	var args []any
	switch f.Role {
	case "admin":
		if f.FilterUserID != nil {
			where += " AND f.user_id = ?"
			args = append(args, *f.FilterUserID)
		}
	case "user":
		if f.UserID != nil {
			where += " AND (f.user_id = ? OR f.is_public = 1)"
			args = append(args, *f.UserID)
		} else {
			where += " AND f.is_public = 1"
		}
	default:
		where += " AND f.is_public = 1"
	}
	if f.Keyword != "" {
		where += ` AND f.file_name LIKE ? ESCAPE '\'`
		args = append(args, "%"+escapeLike(f.Keyword)+"%")
	}

	var total int64
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM files f WHERE `+where, args...).Scan(&total); err != nil {
		return 0, nil, fmt.Errorf("count files: %w", err)
	}
	query := `SELECT ` + fileColumns + ` FROM files f LEFT JOIN users u ON f.user_id = u.id
		WHERE ` + where + ` ORDER BY f.upload_time DESC, f.id DESC LIMIT ? OFFSET ?`
	args = append(args, f.Size, (f.Page-1)*f.Size)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return 0, nil, fmt.Errorf("list files: %w", err)
	}
	files, err := s.scanFiles(rows)
	if err != nil {
		return 0, nil, err
	}
	return total, files, nil
}

// FileByFileID returns the newest row with the given Telegram file id.
func (s *Store) FileByFileID(fileID string) (*model.File, error) {
	return scanFile(s.db.QueryRow(
		`SELECT `+fileColumns+` FROM files f LEFT JOIN users u ON f.user_id = u.id
		 WHERE f.file_id = ? ORDER BY f.id DESC LIMIT 1`, fileID))
}

// FileByWebDAVPath returns the entry stored at a WebDAV path (sql.ErrNoRows if none).
func (s *Store) FileByWebDAVPath(path string) (*model.File, error) {
	return scanFile(s.db.QueryRow(
		`SELECT `+fileColumns+` FROM files f LEFT JOIN users u ON f.user_id = u.id
		 WHERE f.webdav_path = ? ORDER BY f.id DESC LIMIT 1`, path))
}

// FilesByWebDAVPrefix returns entries under a WebDAV path prefix, ordered by path.
func (s *Store) FilesByWebDAVPrefix(prefix string) ([]model.File, error) {
	rows, err := s.db.Query(
		`SELECT `+fileColumns+` FROM files f LEFT JOIN users u ON f.user_id = u.id
		 WHERE f.webdav_path LIKE ? ESCAPE '\' ORDER BY f.webdav_path`,
		escapeLike(prefix)+"%")
	if err != nil {
		return nil, fmt.Errorf("list webdav files: %w", err)
	}
	return s.scanFiles(rows)
}

// UpdateFilePath renames the WebDAV path and display name of a file.
func (s *Store) UpdateFilePath(id int64, webdavPath, fileName string) error {
	_, err := s.db.Exec(`UPDATE files SET webdav_path = ?, file_name = ? WHERE id = ?`, webdavPath, fileName, id)
	if err != nil {
		return fmt.Errorf("update file path: %w", err)
	}
	return nil
}

// UpdateFileWebDAVPath sets only the WebDAV path of a file.
func (s *Store) UpdateFileWebDAVPath(id int64, webdavPath string) error {
	_, err := s.db.Exec(`UPDATE files SET webdav_path = ? WHERE id = ?`, webdavPath, id)
	if err != nil {
		return fmt.Errorf("update webdav path: %w", err)
	}
	return nil
}

// UpdateFilePublic sets the public flag of a file.
func (s *Store) UpdateFilePublic(id int64, public bool) error {
	_, err := s.db.Exec(`UPDATE files SET is_public = ? WHERE id = ?`, public, id)
	if err != nil {
		return fmt.Errorf("update public flag: %w", err)
	}
	return nil
}

// UpdateDownloadURLPrefix rewrites every stored download_url to the given
// public prefix, rebuilding it from the Telegram file id
// (upstream `PUT /api/file-url`).
func (s *Store) UpdateDownloadURLPrefix(newPrefix string) error {
	if _, err := s.db.Exec(
		`UPDATE files SET download_url = ? || '/d/' || file_id
		 WHERE file_id <> '' AND file_id <> 'dir' AND download_url <> ''`,
		newPrefix); err != nil {
		return fmt.Errorf("update download urls: %w", err)
	}
	return nil
}

// CountFiles returns the number of file rows.
func (s *Store) CountFiles() (int64, error) {
	var n int64
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM files`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count files: %w", err)
	}
	return n, nil
}

// DeleteFile removes a file row by id.
func (s *Store) DeleteFile(id int64) error {
	if _, err := s.db.Exec(`DELETE FROM files WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete file: %w", err)
	}
	return nil
}

// DeleteFilesByWebDAVPrefix removes a subtree rooted at prefix.
func (s *Store) DeleteFilesByWebDAVPrefix(prefix string) error {
	_, err := s.db.Exec(`DELETE FROM files WHERE webdav_path LIKE ? ESCAPE '\'`, escapeLike(prefix)+"%")
	if err != nil {
		return fmt.Errorf("delete webdav subtree: %w", err)
	}
	return nil
}

// escapeLike escapes LIKE wildcards in a literal prefix.
func escapeLike(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		switch r {
		case '%', '_', '\\':
			out = append(out, '\\', r)
		default:
			out = append(out, r)
		}
	}
	return string(out)
}
