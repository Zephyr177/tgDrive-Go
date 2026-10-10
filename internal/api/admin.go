package api

import (
	"database/sql"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Zephyr177/tgDrive-Go/internal/bot"
	"github.com/Zephyr177/tgDrive-Go/internal/model"
	"github.com/Zephyr177/tgDrive-Go/internal/store"
)

// configDTO is the upstream ConfigForm wire shape (id intentionally omitted).
type configDTO struct {
	Name   string `json:"name"`
	Token  string `json:"token"`
	Target string `json:"target"`
	URL    string `json:"url"`
	Pass   string `json:"pass"`
}

func toConfigDTO(c store.BotConfig) configDTO {
	return configDTO{Name: c.Name, Token: c.Token, Target: c.Target, URL: c.URL, Pass: c.Pass}
}

// handleRegistrationStatus implements GET /api/setting/registration-status.
func (s *Server) handleRegistrationStatus(w http.ResponseWriter, r *http.Request) {
	allowed := false
	if v, err := s.Store.GetSetting("allow_registration"); err == nil {
		allowed = v == "true"
	}
	writeResult(w, map[string]bool{"isRegistrationAllowed": allowed})
}

// handleListSettings implements GET /api/setting/settings (admin).
func (s *Server) handleListSettings(w http.ResponseWriter, r *http.Request) {
	if s.requireAdmin(w, r) == nil {
		return
	}
	settings, err := s.Store.ListSettings()
	if err != nil {
		writeError(w, "获取设置列表失败")
		return
	}
	if settings == nil {
		settings = []store.Setting{}
	}
	writeResult(w, settings)
}

// handleUpdateSetting implements POST /api/setting (admin).
func (s *Server) handleUpdateSetting(w http.ResponseWriter, r *http.Request) {
	if s.requireAdmin(w, r) == nil {
		return
	}
	var body struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeValidationError(w, "请求体格式错误")
		return
	}
	if strings.TrimSpace(body.Key) == "" {
		writeValidationError(w, "key不能为空")
		return
	}
	if strings.TrimSpace(body.Value) == "" {
		writeValidationError(w, "value不能为空")
		return
	}
	if err := s.Store.UpdateSetting(body.Key, body.Value); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, "该设置不存在")
			return
		}
		writeError(w, "设置更新失败")
		return
	}
	writeResult(w, "设置更新成功")
}

// handleGetConfig implements GET /api/config?name= (admin).
func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	if s.requireAdmin(w, r) == nil {
		return
	}
	name := r.URL.Query().Get("name")
	cfg, err := s.Store.BotConfigByName(name)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, "配置文件不存在")
			return
		}
		writeError(w, "获取配置失败")
		return
	}
	writeResult(w, toConfigDTO(*cfg))
}

// handleListConfigs implements GET /api/config/configs (admin).
func (s *Server) handleListConfigs(w http.ResponseWriter, r *http.Request) {
	if s.requireAdmin(w, r) == nil {
		return
	}
	configs, err := s.Store.ListBotConfigs()
	if err != nil {
		writeError(w, "获取配置列表失败")
		return
	}
	out := make([]configDTO, 0, len(configs))
	for _, c := range configs {
		out = append(out, toConfigDTO(c))
	}
	writeResult(w, out)
}

// handleSaveConfig implements POST /api/config (admin).
func (s *Server) handleSaveConfig(w http.ResponseWriter, r *http.Request) {
	if s.requireAdmin(w, r) == nil {
		return
	}
	var body configDTO
	if err := decodeJSON(r, &body); err != nil {
		writeValidationError(w, "请求体格式错误")
		return
	}
	if strings.TrimSpace(body.Name) == "" {
		writeValidationError(w, "name不能为空")
		return
	}
	if strings.TrimSpace(body.Token) == "" {
		writeValidationError(w, "token不能为空")
		return
	}
	if strings.TrimSpace(body.Target) == "" {
		writeValidationError(w, "target不能为空")
		return
	}
	cfg := store.BotConfig{
		Name:   strings.TrimSpace(body.Name),
		Token:  body.Token,
		Target: body.Target,
		URL:    body.URL,
		Pass:   body.Pass,
	}
	if err := s.Store.UpsertBotConfig(&cfg); err != nil {
		writeError(w, "保存失败")
		return
	}
	writeResult(w, "保存成功")
}

// handleDeleteConfig implements DELETE /api/config/{name} (admin).
func (s *Server) handleDeleteConfig(w http.ResponseWriter, r *http.Request) {
	if s.requireAdmin(w, r) == nil {
		return
	}
	name := r.PathValue("name")
	if strings.TrimSpace(name) == "" {
		writeValidationError(w, "name不能为空")
		return
	}
	if err := s.Store.DeleteBotConfigByName(name); err != nil {
		writeError(w, "删除失败")
		return
	}
	writeResult(w, "删除成功")
}

// handleLoadConfig implements GET /api/config/{name} (admin).
func (s *Server) handleLoadConfig(w http.ResponseWriter, r *http.Request) {
	if s.requireAdmin(w, r) == nil {
		return
	}
	name := r.PathValue("name")
	if strings.TrimSpace(name) == "" {
		writeValidationError(w, "name不能为空")
		return
	}
	if err := s.Bot.Load(name); err != nil {
		writeError(w, err.Error())
		return
	}
	writeResult(w, name)
}

// handleSendMessage implements POST /api/send-message (admin).
func (s *Server) handleSendMessage(w http.ResponseWriter, r *http.Request) {
	if s.requireAdmin(w, r) == nil {
		return
	}
	var body struct {
		Message string `json:"message"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeValidationError(w, "请求体格式错误")
		return
	}
	if strings.TrimSpace(body.Message) == "" {
		writeValidationError(w, "message不能为空")
		return
	}
	if err := s.Bot.SendMessage(body.Message); err != nil {
		writeError(w, err.Error())
		return
	}
	writeResult(w, "发送成功")
}

// handleBackupDownload implements GET /api/backup/download (admin).
func (s *Server) handleBackupDownload(w http.ResponseWriter, r *http.Request) {
	if s.requireAdmin(w, r) == nil {
		return
	}
	if _, err := s.Store.DB().Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		writeError(w, "备份文件不存在")
		return
	}
	f, err := os.Open(s.Cfg.DBPath)
	if err != nil {
		writeError(w, "备份文件不存在")
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		writeError(w, "备份文件不存在")
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment; filename=tgDrive.db")
	http.ServeContent(w, r, "tgDrive.db", info.ModTime(), f)
}

// handleBackupUpload implements POST /api/backup/upload (admin).
func (s *Server) handleBackupUpload(w http.ResponseWriter, r *http.Request) {
	if s.requireAdmin(w, r) == nil {
		return
	}
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		writeError(w, "备份文件格式错误")
		return
	}
	file, header, err := r.FormFile("multipartFile")
	if err != nil {
		writeError(w, "备份文件格式错误")
		return
	}
	defer file.Close()
	if !strings.HasSuffix(strings.ToLower(header.Filename), ".db") {
		writeError(w, "备份文件格式错误")
		return
	}

	// Flush WAL and snapshot the live database before merging.
	if _, err := s.Store.DB().Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		writeError(w, "备份导入失败")
		return
	}
	backupPath := filepath.Join(filepath.Dir(s.Cfg.DBPath), "tgdrive_backup.db")
	if err := copyFileContents(s.Cfg.DBPath, backupPath); err != nil {
		writeError(w, "备份导入失败")
		return
	}

	// Persist the uploaded database to a temp file and open it read-only.
	tmp, err := os.CreateTemp("", "tgdrive-upload-*.db")
	if err != nil {
		writeError(w, "备份导入失败")
		return
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := io.Copy(tmp, file); err != nil {
		tmp.Close()
		writeError(w, "备份导入失败")
		return
	}
	if err := tmp.Close(); err != nil {
		writeError(w, "备份导入失败")
		return
	}

	up, err := sql.Open("sqlite", tmpPath)
	if err != nil {
		writeError(w, "备份导入失败")
		return
	}
	defer up.Close()
	rows, err := up.Query(
		`SELECT file_name, download_url, upload_time, size, full_size, file_id, webdav_path, dir FROM files`)
	if err != nil {
		writeError(w, "备份导入失败")
		return
	}
	defer rows.Close()

	for rows.Next() {
		f := model.File{IsPublic: false}
		if err := rows.Scan(&f.FileName, &f.DownloadURL, &f.UploadTime, &f.Size,
			&f.FullSize, &f.FileID, &f.WebDAVPath, &f.Dir); err != nil {
			continue
		}
		if strings.TrimSpace(f.FileID) == "" {
			continue
		}
		if existing, err := s.Store.FileByFileID(f.FileID); err == nil && existing != nil {
			continue
		} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if _, err := s.Store.InsertFile(&f); err != nil {
			continue
		}
	}
	if err := rows.Err(); err != nil {
		writeError(w, "备份导入失败")
		return
	}
	writeResult(w, "备份导入成功")
}

// copyFileContents copies src to dst, creating/truncating dst.
func copyFileContents(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// handleKeepAliveSend implements POST /api/bot/keep-alive/send (admin).
func (s *Server) handleKeepAliveSend(w http.ResponseWriter, r *http.Request) {
	if s.requireAdmin(w, r) == nil {
		return
	}
	if err := s.Bot.SendMessage(bot.KeepAliveText()); err != nil {
		writeError(w, err.Error())
		return
	}
	writeResult(w, "发送成功")
}

// handleKeepAliveStart implements POST /api/bot/keep-alive/start (admin).
func (s *Server) handleKeepAliveStart(w http.ResponseWriter, r *http.Request) {
	if s.requireAdmin(w, r) == nil {
		return
	}
	s.Bot.Start()
	writeResult(w, "已启动")
}

// handleKeepAliveStop implements POST /api/bot/keep-alive/stop (admin).
func (s *Server) handleKeepAliveStop(w http.ResponseWriter, r *http.Request) {
	if s.requireAdmin(w, r) == nil {
		return
	}
	s.Bot.Stop()
	writeResult(w, "已停止")
}

// handleKeepAliveStatus implements GET /api/bot/keep-alive/status (admin).
func (s *Server) handleKeepAliveStatus(w http.ResponseWriter, r *http.Request) {
	if s.requireAdmin(w, r) == nil {
		return
	}
	writeResult(w, s.Bot.Status())
}

// handleKeepAliveConfig implements GET /api/bot/keep-alive/config (admin).
func (s *Server) handleKeepAliveConfig(w http.ResponseWriter, r *http.Request) {
	if s.requireAdmin(w, r) == nil {
		return
	}
	writeResult(w, s.Bot.ConfigText())
}

// handleGetWebDAVConfig implements GET /api/webdav-config (admin).
func (s *Server) handleGetWebDAVConfig(w http.ResponseWriter, r *http.Request) {
	if s.requireAdmin(w, r) == nil {
		return
	}
	cfg, err := s.Store.GetWebDAVConfig()
	if err != nil {
		writeError(w, "获取WebDAV配置失败")
		return
	}
	writeResult(w, cfg)
}

// handleUpdateWebDAVConfig implements PUT /api/webdav-config (admin).
func (s *Server) handleUpdateWebDAVConfig(w http.ResponseWriter, r *http.Request) {
	if s.requireAdmin(w, r) == nil {
		return
	}
	var cfg store.WebDAVConfig
	if err := decodeJSON(r, &cfg); err != nil {
		writeValidationError(w, "请求体格式错误")
		return
	}
	if strings.TrimSpace(cfg.AllowedRoles) == "" {
		writeValidationError(w, "allowedRoles不能为空")
		return
	}
	cfg.ID = 1
	if err := s.Store.UpdateWebDAVConfig(&cfg); err != nil {
		writeError(w, "更新WebDAV配置失败")
		return
	}
	writeResult(w, nil)
}

// handleWebDAVStatus implements GET /api/webdav-config/status (admin).
func (s *Server) handleWebDAVStatus(w http.ResponseWriter, r *http.Request) {
	if s.requireAdmin(w, r) == nil {
		return
	}
	cfg, err := s.Store.GetWebDAVConfig()
	if err != nil {
		writeError(w, "获取WebDAV状态失败")
		return
	}
	writeResult(w, cfg.Enabled)
}

// handleWebDAVReset implements POST /api/webdav-config/reset (admin).
func (s *Server) handleWebDAVReset(w http.ResponseWriter, r *http.Request) {
	if s.requireAdmin(w, r) == nil {
		return
	}
	if err := s.Store.EnsureWebDAVConfig(); err != nil {
		writeError(w, "重置WebDAV配置失败")
		return
	}
	writeResult(w, nil)
}

// handleSystemMonitor implements GET /api/system/monitor (admin).
func (s *Server) handleSystemMonitor(w http.ResponseWriter, r *http.Request) {
	if s.requireAdmin(w, r) == nil {
		return
	}
	users, err := s.Store.ListUsers()
	if err != nil {
		writeError(w, "获取系统信息失败")
		return
	}
	fileCount, err := s.Store.CountFiles()
	if err != nil {
		writeError(w, "获取系统信息失败")
		return
	}
	writeResult(w, struct {
		UserCount     int    `json:"userCount"`
		FileCount     int64  `json:"fileCount"`
		UptimeSeconds int64  `json:"uptimeSeconds"`
		StartTime     string `json:"startTime"`
	}{
		UserCount:     len(users),
		FileCount:     fileCount,
		UptimeSeconds: int64(time.Since(s.StartTime).Seconds()),
		StartTime:     s.StartTime.Format("2006-01-02 15:04:05"),
	})
}
