package api

import (
	"bytes"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/Zephyr177/tgDrive-Go/internal/files"
	"github.com/Zephyr177/tgDrive-Go/internal/model"
	"github.com/Zephyr177/tgDrive-Go/internal/store"
)

// uploadFile matches the upstream UploadFile DTO.
type uploadFile struct {
	FileName     string `json:"fileName"`
	DownloadLink string `json:"downloadLink"`
}

// handleUpload implements POST /api/upload (multipart field "file").
func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	u := s.requireLogin(w, r)
	if u == nil {
		return
	}
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeError(w, "上传的文件为空！")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, "上传的文件为空！")
		return
	}
	defer file.Close()

	uploaded, err := s.Files.Upload(r.Context(), header.Filename, file, header.Size, &u.ID, requestPrefix(r))
	if err != nil {
		writeError(w, err.Error())
		return
	}
	writeResult(w, uploadFile{FileName: uploaded.FileName, DownloadLink: uploaded.DownloadURL})
}

// handlePermissionCheck implements GET /api/upload/permission-check.
func (s *Server) handlePermissionCheck(w http.ResponseWriter, r *http.Request) {
	if s.requireLogin(w, r) == nil {
		return
	}
	writeResult(w, "OK")
}

// handleFileList implements GET /api/file-list with upstream role filtering.
func (s *Server) handleFileList(w http.ResponseWriter, r *http.Request) {
	page, errPage := strconv.Atoi(r.URL.Query().Get("page"))
	size, errSize := strconv.Atoi(r.URL.Query().Get("size"))
	if errPage != nil || errSize != nil {
		writeValidationError(w, "page和size不能为空")
		return
	}
	filter := store.FileFilter{
		Page:    page,
		Size:    size,
		Keyword: r.URL.Query().Get("keyword"),
	}
	if u := s.currentUser(r); u != nil {
		filter.Role = u.Role
		filter.UserID = &u.ID
	} else {
		filter.Role = "visitor"
	}
	if v := r.URL.Query().Get("userId"); v != "" {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil {
			filter.FilterUserID = &id
		}
	}
	total, records, err := s.Store.ListFilesPaged(filter)
	if err != nil {
		writeError(w, "获取文件列表失败")
		return
	}
	if records == nil {
		records = []model.File{}
	}
	writeResult(w, pageResult{Total: total, Records: records})
}

// handleFileURL implements PUT /api/file-url (admin; re-prefix download URLs).
func (s *Server) handleFileURL(w http.ResponseWriter, r *http.Request) {
	if s.requireAdmin(w, r) == nil {
		return
	}
	if err := s.Store.UpdateDownloadURLPrefix(requestPrefix(r)); err != nil {
		writeError(w, "更新失败")
		return
	}
	writeResult(w, "")
}

// handleFilePublic implements PUT /api/file/{fileId}/public (owner or admin).
func (s *Server) handleFilePublic(w http.ResponseWriter, r *http.Request) {
	u := s.requireLogin(w, r)
	if u == nil {
		return
	}
	var body map[string]bool
	if err := decodeJSON(r, &body); err != nil {
		writeValidationError(w, "请求体格式错误")
		return
	}
	public, ok := body["isPublic"]
	if !ok {
		public = body["public"]
	}

	f, code, err := s.resolveOwnedFile(u, r.PathValue("fileId"))
	if err != nil {
		writeErrorStatusOrBusiness(w, code, err)
		return
	}
	if err := s.Store.UpdateFilePublic(f.ID, public); err != nil {
		writeError(w, "更新失败")
		return
	}
	writeResult(w, "更新成功")
}

// handleDeleteFile implements DELETE /api/file/{fileId} (owner or admin; DB row only).
func (s *Server) handleDeleteFile(w http.ResponseWriter, r *http.Request) {
	u := s.requireLogin(w, r)
	if u == nil {
		return
	}
	f, code, err := s.resolveOwnedFile(u, r.PathValue("fileId"))
	if err != nil {
		writeErrorStatusOrBusiness(w, code, err)
		return
	}
	if err := s.Store.DeleteFile(f.ID); err != nil {
		writeError(w, "文件删除失败")
		return
	}
	writeResult(w, "文件删除成功")
}

// resolveOwnedFile loads a file by its Telegram file id and checks ownership.
func (s *Server) resolveOwnedFile(u *model.User, fileID string) (*model.File, int, error) {
	f, err := s.Store.FileByFileID(fileID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, http.StatusOK, errors.New("文件不存在")
	}
	if err != nil {
		return nil, http.StatusOK, errors.New("操作失败")
	}
	if u.Role != "admin" && (f.UserID == nil || *f.UserID != u.ID) {
		return nil, http.StatusOK, errors.New("无权限操作该文件")
	}
	return f, http.StatusOK, nil
}

// writeErrorStatusOrBusiness writes the resolved error with its status.
func writeErrorStatusOrBusiness(w http.ResponseWriter, status int, err error) {
	if status == http.StatusOK {
		writeError(w, err.Error())
		return
	}
	writeErrorStatus(w, status, err.Error())
}

// handleDownload implements GET /d/{fileID} (public, no is_public check,
// mirroring upstream).
func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	fileID := r.PathValue("fileID")
	var dbRow *model.File
	if f, err := s.Store.FileByFileID(fileID); err == nil {
		dbRow = f
	}
	rc, name, size, err := s.Files.Download(r.Context(), fileID, dbRow)
	if err != nil {
		writeErrorStatus(w, http.StatusNotFound, "文件不存在")
		return
	}
	defer rc.Close()

	peek := make([]byte, 512)
	n, _ := io.ReadFull(rc, peek)
	peek = peek[:n]
	if !strings.Contains(path.Base(name), ".") {
		name = files.AppendExtension(name, peek)
	}
	contentType := files.ContentTypeFor(name)

	w.Header().Set("Content-Type", contentType)
	if size > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	}
	w.Header().Set("Content-Disposition", contentDisposition(name, contentType))
	_, _ = io.Copy(w, io.MultiReader(bytes.NewReader(peek), rc))
}
