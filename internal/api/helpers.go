package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/Zephyr177/tgDrive-Go/internal/model"
)

// response mirrors the upstream Result<T> envelope.
type response struct {
	Code int     `json:"code"`
	Msg  *string `json:"msg"`
	Data any     `json:"data"`
}

// pageResult mirrors the upstream PageResult.
type pageResult struct {
	Total   int64        `json:"total"`
	Records []model.File `json:"records"`
}

func success(data any) response { return response{Code: 1, Data: data} }

func failure(msg string) response {
	return response{Code: 0, Msg: &msg}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json;charset=UTF-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeResult writes a code-1 success with HTTP 200.
func writeResult(w http.ResponseWriter, data any) { writeJSON(w, http.StatusOK, success(data)) }

// writeError writes a code-0 business error with HTTP 200 (upstream style).
func writeError(w http.ResponseWriter, msg string) { writeJSON(w, http.StatusOK, failure(msg)) }

// writeErrorStatus writes a code-0 error with a specific HTTP status.
func writeErrorStatus(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, failure(msg))
}

// writeValidationError writes an HTTP 400 validation error (upstream style).
func writeValidationError(w http.ResponseWriter, msg string) {
	writeJSON(w, http.StatusBadRequest, failure(msg))
}

func decodeJSON(r *http.Request, v any) error {
	return json.NewDecoder(r.Body).Decode(v)
}

// tokenName is the upstream sa-token transport name (header and cookie).
const tokenName = "tgdrive"

// tokenFromRequest resolves the session token: Authorization Bearer (client
// contract), then the sa-token compatible header/cookie/query.
func (s *Server) tokenFromRequest(r *http.Request) string {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
	}
	if v := r.Header.Get(tokenName); v != "" {
		return v
	}
	if c, err := r.Cookie(tokenName); err == nil && c.Value != "" {
		return c.Value
	}
	return r.URL.Query().Get(tokenName)
}

// currentUser resolves the logged-in user, or nil.
func (s *Server) currentUser(r *http.Request) *model.User {
	token := s.tokenFromRequest(r)
	if token == "" {
		return nil
	}
	id, ok := s.Auth.UserID(token)
	if !ok {
		return nil
	}
	u, err := s.Store.UserByID(id)
	if err != nil {
		return nil
	}
	return u
}

// requireLogin writes the upstream 401 when not authenticated.
func (s *Server) requireLogin(w http.ResponseWriter, r *http.Request) *model.User {
	u := s.currentUser(r)
	if u == nil {
		writeErrorStatus(w, http.StatusUnauthorized, "请先登录后再访问")
		return nil
	}
	return u
}

// requireAdmin writes the upstream 401/role error when the user is not admin.
func (s *Server) requireAdmin(w http.ResponseWriter, r *http.Request) *model.User {
	u := s.currentUser(r)
	if u == nil {
		writeErrorStatus(w, http.StatusUnauthorized, "请先登录后再访问")
		return nil
	}
	if u.Role != "admin" {
		writeError(w, "非admin，权限不足")
		return nil
	}
	return u
}

// requestPrefix mirrors upstream StringUtil.getPrefix (honors X-Forwarded-*).
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

// nowString is the upstream last_login_time format.
func nowString() string { return time.Now().Format("2006-01-02 15:04:05") }

// contentDisposition builds the upstream Content-Disposition header value.
func contentDisposition(name, contentType string) string {
	if strings.HasPrefix(contentType, "image/") || strings.HasPrefix(contentType, "video/") {
		return "inline"
	}
	return "attachment; filename*=UTF-8''" + urlEncode(name)
}

func urlEncode(s string) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			c == '-' || c == '_' || c == '.' || c == '~' {
			b.WriteByte(c)
		} else {
			const hex = "0123456789ABCDEF"
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&0xF])
		}
	}
	return b.String()
}
