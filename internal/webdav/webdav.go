// Package webdav implements the /webdav HTTP surface of tgDrive, a Go port of
// the upstream Spring WebDavController + WebDavServiceImpl +
// WebDavFileServiceImpl + WebDavAuthInterceptor stack.
//
// All entries live in the `files` table. Directories are ordinary rows with
// dir=1, file_id='dir', download_url='dir', size='0', is_public=1 and
// user_id NULL, exactly as the upstream Java code stores them. File payloads
// are stored through internal/files (the Telegram backend); the download URL
// is "<url prefix>/d/<fileID>".
//
// The handler is mounted at /webdav by internal/api; it strips that prefix
// before mapping the request onto the virtual tree. Authentication (HTTP
// Basic), the enabled flag and the allowed-roles list are enforced per
// request, mirroring WebDavAuthInterceptor.
package webdav

import (
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Zephyr177/tgDrive-Go/internal/auth"
	"github.com/Zephyr177/tgDrive-Go/internal/files"
	"github.com/Zephyr177/tgDrive-Go/internal/model"
	"github.com/Zephyr177/tgDrive-Go/internal/store"
)

// mountPrefix is the URL path the handler is mounted at (upstream @RequestMapping("/webdav")).
const mountPrefix = "/webdav"

// allowHeader is the upstream OPTIONS Allow value.
const allowHeader = "OPTIONS, HEAD, GET, PUT, DELETE, POST, PROPFIND, MKCOL, MOVE, COPY"

// Handler serves the WebDAV tree.
type Handler struct {
	store *store.Store
	svc   *files.Service
	log   *log.Logger

	// CustomURL, when non-nil and returning a non-empty string, overrides the
	// request origin in generated download URLs. It mirrors upstream
	// TelegramBotService.getCustomUrl() (the loaded bot config's url field),
	// which is not persisted in the Go port; callers may wire it here.
	CustomURL func() string
}

// New returns a WebDAV handler. It performs auth + config checks per request,
// then serves the virtual tree backed by st and svc.
func New(st *store.Store, svc *files.Service, logger *log.Logger) http.Handler {
	return &Handler{store: st, svc: svc, log: logger}
}

func (h *Handler) logf(format string, args ...any) {
	if h.log != nil {
		h.log.Printf(format, args...)
	}
}

// ServeHTTP authenticates the request, then dispatches on the WebDAV method.
// A POST with an X-HTTP-Method-Override header is handled like the upstream
// WebDavFilter/dispatch bridge (path "/webdav/dispatch/...").
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	method := r.Method
	if method == http.MethodPost {
		if override := strings.TrimSpace(r.Header.Get("X-HTTP-Method-Override")); override != "" {
			r = withMethodOverride(r, override)
			method = r.Method
		}
	}

	if !h.authorize(w, r) {
		return
	}

	switch method {
	case http.MethodOptions:
		h.handleOptions(w)
	case http.MethodGet, http.MethodHead:
		h.handleGet(w, r, method == http.MethodHead)
	case http.MethodPut:
		h.handlePut(w, r)
	case http.MethodDelete:
		h.handleDelete(w, r)
	case "PROPFIND":
		h.handlePropfind(w, r)
	case "MKCOL":
		h.handleMkcol(w, r)
	case "MOVE":
		h.handleMove(w, r)
	case "COPY":
		h.handleCopy(w, r)
	case "PROPPATCH":
		h.handleProppatch(w, r)
	case "LOCK", "UNLOCK":
		// Upstream has no LOCK/UNLOCK mapping; replication means unsupported.
		w.WriteHeader(http.StatusNotImplemented)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// withMethodOverride rewrites a POST into the overridden method and removes the
// "/dispatch" segment the upstream WebDavFilter inserts before forwarding.
func withMethodOverride(r *http.Request, override string) *http.Request {
	r2 := r.Clone(r.Context())
	r2.Method = strings.ToUpper(override)
	rel := strings.TrimPrefix(r.URL.Path, mountPrefix)
	if strings.HasPrefix(rel, "/dispatch") {
		rel = strings.TrimPrefix(rel, "/dispatch")
		r2.URL.Path = mountPrefix + rel
		r2.URL.RawPath = ""
	}
	return r2
}

// webdavPath returns the URL-decoded-safe (escaped) virtual path under the
// mount point, always beginning with "/". Upstream keeps the raw request URI
// (URL-encoded) as the stored path, so the escaped form is used for lookups.
func (h *Handler) webdavPath(r *http.Request) string {
	p := r.URL.EscapedPath()
	if p == "" {
		p = "/"
	}
	p = strings.TrimPrefix(p, mountPrefix)
	if p == "" {
		p = "/"
	}
	return p
}

// authorize enforces the WebDavAuthInterceptor rules in order: enabled flag
// (503), Basic credentials (401), allowed role (403).
func (h *Handler) authorize(w http.ResponseWriter, r *http.Request) bool {
	cfg, err := h.store.GetWebDAVConfig()
	if err != nil {
		h.logf("webdav: load config: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		return false
	}
	if !cfg.Enabled {
		w.WriteHeader(http.StatusServiceUnavailable)
		return false
	}

	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Basic ") {
		writeUnauthorized(w)
		return false
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(header, "Basic "))
	if err != nil {
		writeUnauthorized(w)
		return false
	}
	username, password, ok := strings.Cut(string(decoded), ":")
	if !ok {
		writeUnauthorized(w)
		return false
	}

	user, err := h.store.UserByUsernameOrEmail(username)
	if err != nil || !auth.CheckPassword(user.Password, password) {
		h.logf("webdav: bad credentials for %q", username)
		writeUnauthorized(w)
		return false
	}
	if !roleAllowed(cfg.AllowedRoles, user.Role) {
		h.logf("webdav: role %q not allowed", user.Role)
		w.WriteHeader(http.StatusForbidden)
		return false
	}
	return true
}

func writeUnauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Basic realm="WebDAV"`)
	w.WriteHeader(http.StatusUnauthorized)
}

// roleAllowed reports whether role is in the comma-separated allowed list;
// "all" (as a whole list or an entry) means everyone.
func roleAllowed(allowed, role string) bool {
	for _, part := range strings.Split(allowed, ",") {
		switch strings.TrimSpace(part) {
		case "all", role:
			return true
		}
	}
	return false
}

// lookup resolves a WebDAV path to a file row. It tries the raw path first,
// then the URL-decoded path, then a case-insensitive parent-directory scan,
// mirroring getFileByWebdavPathWithFallback. Returns nil when absent.
func (h *Handler) lookup(path string) *model.File {
	if f, err := h.store.FileByWebDAVPath(path); err == nil {
		return f
	}
	if decoded, err := url.PathUnescape(path); err == nil && decoded != path {
		if f, err := h.store.FileByWebDAVPath(decoded); err == nil {
			return f
		}
	}
	parent := path
	if i := strings.LastIndex(path, "/"); i >= 0 {
		parent = path[:i+1]
	} else {
		parent = "/"
	}
	rows, err := h.store.FilesByWebDAVPrefix(parent)
	if err != nil {
		return nil
	}
	for i := range rows {
		if strings.EqualFold(rows[i].WebDAVPath, path) {
			return &rows[i]
		}
	}
	return nil
}

// handleOptions sets the upstream probe headers.
func (h *Handler) handleOptions(w http.ResponseWriter) {
	w.Header().Set("Allow", allowHeader)
	w.Header().Set("DAV", "1,2")
	w.Header().Set("MS-Author-Via", "DAV")
	w.WriteHeader(http.StatusOK)
}

// handleGet streams a stored file (WebDavFileServiceImpl.downloadByWebDav).
func (h *Handler) handleGet(w http.ResponseWriter, r *http.Request, headOnly bool) {
	path := h.webdavPath(r)
	f := h.lookup(path)
	if f == nil {
		http.NotFound(w, r)
		return
	}
	if f.Dir {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	rc, name, size, err := h.svc.Download(r.Context(), f.FileID, f)
	if err != nil {
		h.logf("webdav: download %q: %v", path, err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	defer rc.Close()
	if name == "" {
		name = f.FileName
	}
	contentType := files.ContentTypeFor(name)
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", contentDisposition(name, contentType))
	if size > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	}
	if headOnly {
		w.WriteHeader(http.StatusOK)
		return
	}
	if _, err := io.Copy(w, rc); err != nil {
		h.logf("webdav: stream %q: %v", path, err)
	}
}

// handlePut stores a file body through the files service and marks the row
// public, matching WebDavFileServiceImpl.uploadByWebDav.
func (h *Handler) handlePut(w http.ResponseWriter, r *http.Request) {
	path := h.webdavPath(r)
	if strings.HasSuffix(path, "/") {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	size := r.ContentLength
	if size < 0 {
		h.logf("webdav: put %q: unknown content length", path)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	name := displayName(path, false)

	// Upstream deletes every existing row at/under the path before inserting
	// the replacement; the files service inserts the new row itself, so the
	// removal has to happen first.
	if err := h.store.DeleteFilesByWebDAVPrefix(path); err != nil {
		h.logf("webdav: put %q: clear old rows: %v", path, err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	h.ensureDirs(path)

	f, err := h.svc.Upload(r.Context(), name, r.Body, size, nil, h.urlPrefix(r))
	if err != nil {
		h.logf("webdav: put %q: %v", path, err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if err := h.store.UpdateFileWebDAVPath(f.ID, path); err != nil {
		h.logf("webdav: put %q: set path: %v", path, err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if err := h.store.UpdateFilePublic(f.ID, true); err != nil {
		h.logf("webdav: put %q: mark public: %v", path, err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

// ensureDirs inserts any missing parent directory rows for a path.
func (h *Handler) ensureDirs(path string) {
	for _, dirPath := range dirsPath(path) {
		if h.lookup(dirPath) != nil {
			continue
		}
		dir := &model.File{
			FileName:    displayName(dirPath, true),
			DownloadURL: "dir",
			UploadTime:  time.Now().Unix(),
			Size:        "0",
			FileID:      "dir",
			WebDAVPath:  dirPath,
			Dir:         true,
			IsPublic:    true,
		}
		if _, err := h.store.InsertFile(dir); err != nil {
			h.logf("webdav: create dir %q: %v", dirPath, err)
		}
	}
}

// handleDelete removes the rows rooted at the path (upstream deleteByWebDav,
// which matches by path prefix). Always answers 204.
func (h *Handler) handleDelete(w http.ResponseWriter, r *http.Request) {
	path := h.webdavPath(r)
	if err := h.store.DeleteFilesByWebDAVPrefix(path); err != nil {
		h.logf("webdav: delete %q: %v", path, err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleMkcol creates a directory row, 405 when the path already exists.
func (h *Handler) handleMkcol(w http.ResponseWriter, r *http.Request) {
	path := h.webdavPath(r)
	if h.lookup(path) != nil {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	dir := &model.File{
		FileName:    displayName(path, true),
		DownloadURL: "dir",
		UploadTime:  time.Now().Unix(),
		Size:        "0",
		FileID:      "dir",
		WebDAVPath:  path,
		Dir:         true,
		IsPublic:    true,
	}
	if _, err := h.store.InsertFile(dir); err != nil {
		h.logf("webdav: mkcol %q: %v", path, err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

// handleMove renames the source row and its subtree to the Destination path.
func (h *Handler) handleMove(w http.ResponseWriter, r *http.Request) {
	srcPath := h.webdavPath(r)
	src := h.lookup(srcPath)
	dest := r.Header.Get("Destination")
	if src == nil || dest == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	target, ok := h.targetPath(dest, src.Dir)
	if !ok {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if target == srcPath {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	overwrite := r.Header.Get("Overwrite")
	targetFile := h.lookup(target)
	if targetFile != nil && strings.EqualFold(overwrite, "F") {
		w.WriteHeader(http.StatusConflict)
		return
	}
	if targetFile != nil {
		if err := h.store.DeleteFilesByWebDAVPrefix(target); err != nil {
			h.logf("webdav: move %q: clear target: %v", srcPath, err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
	}
	rows, err := h.store.FilesByWebDAVPrefix(srcPath)
	if err != nil {
		h.logf("webdav: move %q: %v", srcPath, err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	for i := range rows {
		row := rows[i]
		newPath := target + strings.TrimPrefix(row.WebDAVPath, srcPath)
		name := row.FileName
		if row.WebDAVPath == srcPath {
			name = displayName(target, row.Dir)
		}
		if err := h.store.UpdateFilePath(row.ID, newPath, name); err != nil {
			h.logf("webdav: move %q -> %q: %v", row.WebDAVPath, newPath, err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleCopy duplicates the source row and its subtree at the Destination
// path. Upstream approximates COPY by moving rows; we duplicate them so the
// source survives, which is the observable WebDAV contract.
func (h *Handler) handleCopy(w http.ResponseWriter, r *http.Request) {
	srcPath := h.webdavPath(r)
	src := h.lookup(srcPath)
	dest := r.Header.Get("Destination")
	if src == nil || dest == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	target, ok := h.targetPath(dest, src.Dir)
	if !ok {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if target == srcPath {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	overwrite := r.Header.Get("Overwrite")
	targetFile := h.lookup(target)
	if targetFile != nil && strings.EqualFold(overwrite, "F") {
		w.WriteHeader(http.StatusConflict)
		return
	}
	if targetFile != nil {
		if err := h.store.DeleteFilesByWebDAVPrefix(target); err != nil {
			h.logf("webdav: copy %q: clear target: %v", srcPath, err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
	}
	rows, err := h.store.FilesByWebDAVPrefix(srcPath)
	if err != nil {
		h.logf("webdav: copy %q: %v", srcPath, err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	for i := range rows {
		clone := rows[i]
		newPath := target + strings.TrimPrefix(clone.WebDAVPath, srcPath)
		if clone.WebDAVPath == srcPath {
			clone.FileName = displayName(target, clone.Dir)
		}
		clone.ID = 0
		clone.WebDAVPath = newPath
		if _, err := h.store.InsertFile(&clone); err != nil {
			h.logf("webdav: copy %q -> %q: %v", rows[i].WebDAVPath, newPath, err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// targetPath extracts the virtual path from a Destination header (an absolute
// URL) and appends "/" when the source is a collection.
func (h *Handler) targetPath(destination string, isDir bool) (string, bool) {
	u, err := url.Parse(destination)
	if err != nil {
		return "", false
	}
	p := u.EscapedPath()
	if !strings.HasPrefix(p, mountPrefix) {
		return "", false
	}
	p = strings.TrimPrefix(p, mountPrefix)
	if p == "" {
		p = "/"
	}
	if isDir && !strings.HasSuffix(p, "/") {
		p += "/"
	}
	return p, true
}

// handlePropfind emits a 207 multistatus listing the resource and, for
// collections, its direct children (upstream handlePropFind).
func (h *Handler) handlePropfind(w http.ResponseWriter, r *http.Request) {
	path := h.webdavPath(r)
	var current *model.File
	if path != "/" {
		current = h.lookup(path)
		if current == nil {
			http.NotFound(w, r)
			return
		}
	}

	var children []model.File
	if path == "/" || (current != nil && current.Dir) {
		scanPrefix := path
		if path != "/" && !strings.HasSuffix(scanPrefix, "/") {
			scanPrefix += "/"
		}
		rows, err := h.store.FilesByWebDAVPrefix(scanPrefix)
		if err != nil {
			h.logf("webdav: propfind %q: %v", path, err)
		}
		for _, row := range rows {
			if directChild(scanPrefix, row.WebDAVPath) {
				children = append(children, row)
			}
		}
	}

	var b strings.Builder
	b.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n")
	b.WriteString("<D:multistatus xmlns:D=\"DAV:\">\n")

	b.WriteString("<D:response>\n")
	b.WriteString("<D:href>" + escapeXML(mountPrefix+path) + "</D:href>\n")
	b.WriteString("<D:propstat>\n<D:prop>\n")
	// Upstream getDisplayName(path) returns the trailing "/segment" including
	// the leading slash (so "/" for the root).
	b.WriteString("<D:displayname>" + escapeXML(path[strings.LastIndex(path, "/"):]) + "</D:displayname>\n")
	modified := time.Now().Unix()
	if path != "/" && current != nil {
		modified = current.UploadTime
	}
	b.WriteString("<D:getlastmodified>" + time.Unix(modified, 0).UTC().Format(http.TimeFormat) + "</D:getlastmodified>\n")
	if path == "/" || (current != nil && current.Dir) {
		b.WriteString("<D:resourcetype><D:collection/></D:resourcetype>\n")
	} else {
		b.WriteString("<D:resourcetype/>\n")
		b.WriteString("<D:getcontentlength>" + strconv.FormatInt(current.FullSize, 10) + "</D:getcontentlength>\n")
	}
	b.WriteString("</D:prop>\n<D:status>HTTP/1.1 200 OK</D:status>\n</D:propstat>\n</D:response>\n")

	for i := range children {
		child := children[i]
		relative := path
		if strings.HasSuffix(relative, "/") {
			relative += child.FileName
		} else {
			relative += "/" + child.FileName
		}
		b.WriteString("<D:response>\n")
		b.WriteString("<D:href>" + escapeXML(mountPrefix+relative) + "</D:href>\n")
		b.WriteString("<D:propstat>\n<D:prop>\n")
		b.WriteString("<D:displayname>" + escapeXML(child.FileName) + "</D:displayname>\n")
		b.WriteString("<D:getlastmodified>" + time.Unix(child.UploadTime, 0).UTC().Format(http.TimeFormat) + "</D:getlastmodified>\n")
		if child.Dir {
			b.WriteString("<D:resourcetype><D:collection/></D:resourcetype>\n")
		} else {
			b.WriteString("<D:resourcetype/>\n")
			b.WriteString("<D:getcontentlength>" + strconv.FormatInt(child.FullSize, 10) + "</D:getcontentlength>\n")
		}
		b.WriteString("</D:prop>\n<D:status>HTTP/1.1 200 OK</D:status>\n</D:propstat>\n</D:response>\n")
	}
	b.WriteString("</D:multistatus>")

	w.Header().Set("Content-Type", "application/xml;charset=UTF-8")
	w.WriteHeader(207)
	io.WriteString(w, b.String())
}

// handleProppatch pretends the property update succeeded (upstream returns a
// canned 207 because the server does not persist properties).
func (h *Handler) handleProppatch(w http.ResponseWriter, r *http.Request) {
	path := h.webdavPath(r)
	body := "<?xml version=\"1.0\" encoding=\"utf-8\"?>" +
		"<D:multistatus xmlns:D=\"DAV:\">" +
		"  <D:response>" +
		"    <D:href>" + escapeXML(mountPrefix+path) + "</D:href>" +
		"    <D:propstat>" +
		"      <D:status>HTTP/1.1 200 OK</D:status>" +
		"    </D:propstat>" +
		"  </D:response>" +
		"</D:multistatus>"
	w.Header().Set("Content-Type", "application/xml;charset=UTF-8")
	w.WriteHeader(207)
	io.WriteString(w, body)
}

// urlPrefix returns the public URL prefix for generated download links: the
// configured custom URL when available, otherwise the request origin.
func (h *Handler) urlPrefix(r *http.Request) string {
	if h.CustomURL != nil {
		if u := strings.TrimSpace(h.CustomURL()); u != "" {
			return u
		}
	}
	return requestPrefix(r)
}

// displayName mirrors StringUtil.getDisplayName: for a collection it returns
// the last non-empty segment (paths end with "/"), for a file the basename.
func displayName(path string, dir bool) string {
	if dir {
		path = path[:strings.LastIndex(path, "/")]
	}
	return path[strings.LastIndex(path, "/")+1:]
}

// dirsPath mirrors StringUtil.getDirsPathFromPath: every ancestor collection
// path ("/a/", "/a/b/") implied by a file path.
func dirsPath(path string) []string {
	parts := strings.Split(path, "/")
	if len(parts) > 0 && strings.Contains(parts[len(parts)-1], ".") {
		parts = parts[:len(parts)-1]
	}
	var out []string
	var current strings.Builder
	for _, part := range parts {
		if part == "" {
			continue
		}
		if strings.Contains(part, ".") {
			break
		}
		current.WriteString("/" + part)
		out = append(out, current.String()+"/")
	}
	return out
}

// directChild reports whether child is an immediate child of the collection
// prefix parent (which ends with "/").
func directChild(parent, child string) bool {
	if child == parent {
		return false
	}
	remainder := strings.TrimSuffix(strings.TrimPrefix(child, parent), "/")
	return remainder != "" && !strings.Contains(remainder, "/")
}

// escapeXML escapes the five XML special characters, as upstream escapeXml does.
func escapeXML(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#39;")
	return r.Replace(s)
}

// contentDisposition mirrors the upstream download disposition choice.
func contentDisposition(name, contentType string) string {
	if strings.HasPrefix(contentType, "image/") || strings.HasPrefix(contentType, "video/") {
		return "inline"
	}
	return fmt.Sprintf("attachment; filename*=UTF-8''%s", urlEncode(name))
}

// urlEncode percent-encodes a file name for the Content-Disposition header.
func urlEncode(s string) string {
	var b strings.Builder
	for _, r := range []byte(s) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'),
			r == '-', r == '_', r == '.', r == '~':
			b.WriteByte(r)
		default:
			fmt.Fprintf(&b, "%%%02X", r)
		}
	}
	return b.String()
}

// requestPrefix mirrors upstream StringUtil.getPrefix and the api package
// helper: honors X-Forwarded-Proto / X-Forwarded-Port and elides default ports.
func requestPrefix(r *http.Request) string {
	proto := r.Header.Get("X-Forwarded-Proto")
	if proto == "" {
		proto = "http"
		if r.TLS != nil {
			proto = "https"
		}
	}
	host := r.Host
	if port := r.Header.Get("X-Forwarded-Port"); port != "" {
		if i := strings.LastIndex(host, ":"); i >= 0 {
			host = host[:i]
		}
		host += ":" + port
	}
	switch {
	case proto == "http" && strings.HasSuffix(host, ":80"):
		host = strings.TrimSuffix(host, ":80")
	case proto == "https" && strings.HasSuffix(host, ":443"):
		host = strings.TrimSuffix(host, ":443")
	}
	return proto + "://" + host
}
