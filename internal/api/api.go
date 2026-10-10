// Package api implements the HTTP surface of tgDrive.
package api

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/Zephyr177/tgDrive-Go/internal/auth"
	"github.com/Zephyr177/tgDrive-Go/internal/bot"
	"github.com/Zephyr177/tgDrive-Go/internal/config"
	"github.com/Zephyr177/tgDrive-Go/internal/files"
	"github.com/Zephyr177/tgDrive-Go/internal/store"
	"github.com/Zephyr177/tgDrive-Go/internal/ws"
)

// Server bundles the HTTP dependencies of tgDrive.
type Server struct {
	Cfg       config.Config
	Store     *store.Store
	Auth      *auth.Manager
	Files     *files.Service
	Hub       *ws.Hub
	Bot       *bot.Controller
	WebDAV    http.Handler // mounted at /webdav; nil disables
	UIFS      fs.FS        // embedded UI assets (index.html at root)
	StartTime time.Time
}

// NewServer returns a server with the given dependencies.
func NewServer(cfg config.Config, st *store.Store, am *auth.Manager, fsvc *files.Service,
	hub *ws.Hub, bc *bot.Controller, webdav http.Handler, uiFS fs.FS) *Server {
	return &Server{
		Cfg:       cfg,
		Store:     st,
		Auth:      am,
		Files:     fsvc,
		Hub:       hub,
		Bot:       bc,
		WebDAV:    webdav,
		UIFS:      uiFS,
		StartTime: time.Now(),
	}
}

// Routes builds the HTTP handler tree.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	// Authentication & users (upstream /api/auth).
	mux.HandleFunc("POST /api/auth/login", s.handleLogin)
	mux.HandleFunc("POST /api/auth/register", s.handleRegister)
	mux.HandleFunc("POST /api/auth/change-password", s.handleChangePassword)
	mux.HandleFunc("POST /api/auth/logout", s.handleLogout)
	mux.HandleFunc("POST /api/auth/admin/change-password", s.handleAdminChangePassword)
	mux.HandleFunc("GET /api/auth/admin/users", s.handleListUsers)
	mux.HandleFunc("DELETE /api/auth/admin/users/{userId}", s.handleDeleteUser)
	mux.HandleFunc("PUT /api/auth/admin/users/{userId}/role", s.handleUpdateUserRole)

	// Files (upstream FileController + DownloadController).
	mux.HandleFunc("POST /api/upload", s.handleUpload)
	mux.HandleFunc("GET /api/upload/permission-check", s.handlePermissionCheck)
	mux.HandleFunc("GET /api/file-list", s.handleFileList)
	mux.HandleFunc("PUT /api/file-url", s.handleFileURL)
	mux.HandleFunc("PUT /api/file/{fileId}/public", s.handleFilePublic)
	mux.HandleFunc("DELETE /api/file/{fileId}", s.handleDeleteFile)
	mux.HandleFunc("GET /d/{fileID}", s.handleDownload)

	// Settings, bot configs, messages.
	mux.HandleFunc("GET /api/setting/registration-status", s.handleRegistrationStatus)
	mux.HandleFunc("GET /api/setting/settings", s.handleListSettings)
	mux.HandleFunc("POST /api/setting", s.handleUpdateSetting)
	mux.HandleFunc("GET /api/config", s.handleGetConfig)
	mux.HandleFunc("GET /api/config/configs", s.handleListConfigs)
	mux.HandleFunc("POST /api/config", s.handleSaveConfig)
	mux.HandleFunc("DELETE /api/config/{name}", s.handleDeleteConfig)
	mux.HandleFunc("GET /api/config/{name}", s.handleLoadConfig)
	mux.HandleFunc("POST /api/send-message", s.handleSendMessage)

	// Backup.
	mux.HandleFunc("GET /api/backup/download", s.handleBackupDownload)
	mux.HandleFunc("POST /api/backup/upload", s.handleBackupUpload)

	// Bot keep-alive.
	mux.HandleFunc("POST /api/bot/keep-alive/send", s.handleKeepAliveSend)
	mux.HandleFunc("POST /api/bot/keep-alive/start", s.handleKeepAliveStart)
	mux.HandleFunc("POST /api/bot/keep-alive/stop", s.handleKeepAliveStop)
	mux.HandleFunc("GET /api/bot/keep-alive/status", s.handleKeepAliveStatus)
	mux.HandleFunc("GET /api/bot/keep-alive/config", s.handleKeepAliveConfig)

	// WebDAV configuration.
	mux.HandleFunc("GET /api/webdav-config", s.handleGetWebDAVConfig)
	mux.HandleFunc("PUT /api/webdav-config", s.handleUpdateWebDAVConfig)
	mux.HandleFunc("GET /api/webdav-config/status", s.handleWebDAVStatus)
	mux.HandleFunc("POST /api/webdav-config/reset", s.handleWebDAVReset)

	// Operational extras.
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /api/system/monitor", s.handleSystemMonitor)

	// Realtime upload progress.
	mux.HandleFunc("GET /ws/upload-progress", s.Hub.Handler())

	// WebDAV surface.
	if s.WebDAV != nil {
		mux.Handle("/webdav", s.WebDAV)
		mux.Handle("/webdav/", s.WebDAV)
	}

	// Everything else: embedded UI with SPA fallback.
	mux.Handle("/", s.uiHandler())

	return cors(mux)
}

// cors applies the upstream /api/** CORS policy (any origin, no credentials).
func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "*")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// uiHandler serves the embedded UI, falling back to index.html for
// extension-less routes (upstream ViewController behavior).
func (s *Server) uiHandler() http.Handler {
	fileServer := http.FileServerFS(s.UIFS)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := path.Clean(r.URL.Path)
		if p == "/" {
			http.ServeFileFS(w, r, s.UIFS, "index.html")
			return
		}
		name := strings.TrimPrefix(p, "/")
		if f, err := s.UIFS.Open(name); err == nil {
			f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}
		if !strings.Contains(path.Base(p), ".") {
			http.ServeFileFS(w, r, s.UIFS, "index.html")
			return
		}
		http.NotFound(w, r)
	})
}
