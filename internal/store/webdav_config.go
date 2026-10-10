package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// webdavDefaultTime is the seed timestamp used by the upstream migration.
const webdavDefaultTime = int64(1642694400000)

// WebDAVConfig mirrors the webdav_config table (single row, id=1).
type WebDAVConfig struct {
	ID           int64  `json:"id"`
	Enabled      bool   `json:"enabled"`
	RequireAuth  bool   `json:"requireAuth"`
	AllowedRoles string `json:"allowedRoles"`
	AllowMkdir   bool   `json:"allowMkdir"`
	AllowDelete  bool   `json:"allowDelete"`
	AllowMove    bool   `json:"allowMove"`
	AllowCopy    bool   `json:"allowCopy"`
	Description  string `json:"description"`
	CreateTime   int64  `json:"createTime"`
	UpdateTime   int64  `json:"updateTime"`
}

// EnsureWebDAVConfig inserts the default row when missing.
func (s *Store) EnsureWebDAVConfig() error {
	_, err := s.db.Exec(
		`INSERT OR IGNORE INTO webdav_config
		 (id, enabled, require_auth, allowed_roles, allow_mkdir, allow_delete, allow_move, allow_copy, description, create_time, update_time)
		 VALUES (1, 1, 1, 'admin', 1, 1, 1, 1, 'WebDAV服务配置', ?, ?)`,
		webdavDefaultTime, webdavDefaultTime)
	if err != nil {
		return fmt.Errorf("ensure webdav config: %w", err)
	}
	return nil
}

// GetWebDAVConfig returns the config row (creating the default if missing).
func (s *Store) GetWebDAVConfig() (*WebDAVConfig, error) {
	var c WebDAVConfig
	err := s.db.QueryRow(
		`SELECT id, enabled, require_auth, allowed_roles, allow_mkdir, allow_delete, allow_move, allow_copy,
		        COALESCE(description, ''), create_time, update_time
		 FROM webdav_config WHERE id = 1`).
		Scan(&c.ID, &c.Enabled, &c.RequireAuth, &c.AllowedRoles, &c.AllowMkdir, &c.AllowDelete,
			&c.AllowMove, &c.AllowCopy, &c.Description, &c.CreateTime, &c.UpdateTime)
	if errors.Is(err, sql.ErrNoRows) {
		if err := s.EnsureWebDAVConfig(); err != nil {
			return nil, err
		}
		return s.GetWebDAVConfig()
	}
	if err != nil {
		return nil, fmt.Errorf("get webdav config: %w", err)
	}
	return &c, nil
}

// UpdateWebDAVConfig stores the flags and refreshes update_time (epoch millis).
func (s *Store) UpdateWebDAVConfig(c *WebDAVConfig) error {
	_, err := s.db.Exec(
		`UPDATE webdav_config SET enabled = ?, require_auth = ?, allowed_roles = ?, allow_mkdir = ?,
		 allow_delete = ?, allow_move = ?, allow_copy = ?, description = ?, update_time = ? WHERE id = 1`,
		c.Enabled, c.RequireAuth, c.AllowedRoles, c.AllowMkdir, c.AllowDelete, c.AllowMove, c.AllowCopy,
		c.Description, time.Now().UnixMilli())
	if err != nil {
		return fmt.Errorf("update webdav config: %w", err)
	}
	return nil
}
