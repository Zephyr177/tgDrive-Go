package webdav

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zephyr177/tgDrive-Go/internal/files"
	"github.com/Zephyr177/tgDrive-Go/internal/store"
	"github.com/Zephyr177/tgDrive-Go/internal/telegram"
)

// newTestHandler builds a handler backed by a temp SQLite store and the fake
// Telegram backend, mirroring the production wiring.
func newTestHandler(t *testing.T) (http.Handler, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	svc := files.New(st, telegram.NewFake(), nil)
	return New(st, svc, nil), st
}

// do issues a request against the handler; auth=true sends the seeded admin
// credentials (admin/123456).
func do(h http.Handler, method, target string, body []byte, auth bool) *httptest.ResponseRecorder {
	var r *http.Request
	if body == nil {
		r = httptest.NewRequest(method, target, nil)
	} else {
		r = httptest.NewRequest(method, target, bytes.NewReader(body))
	}
	if auth {
		r.SetBasicAuth("admin", "123456")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestUnauthorizedWithoutBasicAuth(t *testing.T) {
	h, _ := newTestHandler(t)
	w := do(h, "PROPFIND", "/webdav/", nil, false)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
	if got := w.Header().Get("WWW-Authenticate"); got != `Basic realm="WebDAV"` {
		t.Fatalf("WWW-Authenticate = %q", got)
	}
}

func TestPutThenGetReturnsIdenticalBytes(t *testing.T) {
	h, _ := newTestHandler(t)
	payload := []byte("hello webdav world\n")

	if w := do(h, http.MethodPut, "/webdav/hello.txt", payload, true); w.Code != http.StatusCreated {
		t.Fatalf("PUT status = %d, want 201", w.Code)
	}
	w := do(h, http.MethodGet, "/webdav/hello.txt", nil, true)
	if w.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", w.Code)
	}
	if !bytes.Equal(w.Body.Bytes(), payload) {
		t.Fatalf("GET body = %q, want %q", w.Body.Bytes(), payload)
	}
}

func TestPropfindRootListsUploadedFile(t *testing.T) {
	h, _ := newTestHandler(t)
	if w := do(h, http.MethodPut, "/webdav/hello.txt", []byte("data"), true); w.Code != http.StatusCreated {
		t.Fatalf("PUT status = %d, want 201", w.Code)
	}
	w := do(h, "PROPFIND", "/webdav/", nil, true)
	if w.Code != 207 {
		t.Fatalf("PROPFIND status = %d, want 207", w.Code)
	}
	if body := w.Body.String(); !strings.Contains(body, "hello.txt") {
		t.Fatalf("PROPFIND body does not list hello.txt: %s", body)
	}
}

func TestMkcolThenPropfindShowsCollection(t *testing.T) {
	h, _ := newTestHandler(t)
	if w := do(h, "MKCOL", "/webdav/docs/", nil, true); w.Code != http.StatusCreated {
		t.Fatalf("MKCOL status = %d, want 201", w.Code)
	}
	if w := do(h, "MKCOL", "/webdav/docs/", nil, true); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("MKCOL again status = %d, want 405", w.Code)
	}
	w := do(h, "PROPFIND", "/webdav/", nil, true)
	if w.Code != 207 {
		t.Fatalf("PROPFIND status = %d, want 207", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "docs") || !strings.Contains(body, "<D:collection/>") {
		t.Fatalf("PROPFIND body missing collection: %s", body)
	}
}

func TestDeleteRemovesFile(t *testing.T) {
	h, _ := newTestHandler(t)
	if w := do(h, http.MethodPut, "/webdav/gone.txt", []byte("bye"), true); w.Code != http.StatusCreated {
		t.Fatalf("PUT status = %d, want 201", w.Code)
	}
	if w := do(h, http.MethodDelete, "/webdav/gone.txt", nil, true); w.Code != http.StatusNoContent {
		t.Fatalf("DELETE status = %d, want 204", w.Code)
	}
	if w := do(h, http.MethodGet, "/webdav/gone.txt", nil, true); w.Code != http.StatusNotFound {
		t.Fatalf("GET after DELETE status = %d, want 404", w.Code)
	}
}

func TestDisabledConfigReturns503(t *testing.T) {
	h, st := newTestHandler(t)
	cfg, err := st.GetWebDAVConfig()
	if err != nil {
		t.Fatalf("get config: %v", err)
	}
	cfg.Enabled = false
	if err := st.UpdateWebDAVConfig(cfg); err != nil {
		t.Fatalf("update config: %v", err)
	}
	w := do(h, "PROPFIND", "/webdav/", nil, true)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
}
