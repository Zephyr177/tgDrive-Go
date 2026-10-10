// Package store provides the SQLite persistence layer.
package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite" // pure-Go sqlite driver

	"github.com/Zephyr177/tgDrive-Go/internal/auth"
)

// schemaSQL is the tgDrive schema matching the final upstream state after
// Flyway V1..V12 (users / files / configs / settings / webdav_config).
const schemaSQL = `
CREATE TABLE IF NOT EXISTS users (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    username TEXT NOT NULL UNIQUE,
    password TEXT NOT NULL,
    role TEXT NOT NULL,
    last_login_time TEXT DEFAULT NULL,
    reserved_2 TEXT DEFAULT NULL,
    reserved_3 TEXT DEFAULT NULL,
    email TEXT DEFAULT NULL
);

CREATE TABLE IF NOT EXISTS files (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    file_name TEXT NOT NULL,
    download_url TEXT NOT NULL DEFAULT '',
    upload_time INTEGER NOT NULL,
    size TEXT NOT NULL DEFAULT '',
    full_size INTEGER DEFAULT 0,
    file_id TEXT NOT NULL DEFAULT '',
    webdav_path TEXT DEFAULT '',
    dir BOOLEAN DEFAULT FALSE,
    is_public BOOLEAN DEFAULT FALSE,
    user_id INTEGER,
    messageID INTEGER
);

CREATE TABLE IF NOT EXISTS configs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    token TEXT NOT NULL DEFAULT '',
    target TEXT NOT NULL DEFAULT '',
    url TEXT,
    pass TEXT
);

CREATE TABLE IF NOT EXISTS settings (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    key TEXT NOT NULL UNIQUE,
    value TEXT NOT NULL,
    description TEXT
);

CREATE TABLE IF NOT EXISTS webdav_config (
    id INTEGER PRIMARY KEY,
    enabled BOOLEAN NOT NULL DEFAULT 1,
    require_auth BOOLEAN NOT NULL DEFAULT 1,
    allowed_roles TEXT NOT NULL DEFAULT 'admin',
    allow_mkdir BOOLEAN NOT NULL DEFAULT 1,
    allow_delete BOOLEAN NOT NULL DEFAULT 1,
    allow_move BOOLEAN NOT NULL DEFAULT 1,
    allow_copy BOOLEAN NOT NULL DEFAULT 1,
    description TEXT,
    create_time INTEGER NOT NULL,
    update_time INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_files_webdav_path ON files(webdav_path);
CREATE INDEX IF NOT EXISTS idx_files_dir ON files(dir);
CREATE INDEX IF NOT EXISTS idx_files_public ON files(is_public);
CREATE INDEX IF NOT EXISTS idx_files_user ON files(user_id);
`

// Default seeded accounts (same logins as upstream).
var defaultUsers = []struct{ name, password, role string }{
	{"admin", "123456", "admin"},
	{"visitor", "hello", "visitor"},
}

// Store wraps the SQLite database.
type Store struct {
	db *sql.DB
}

// Open opens (creating if needed) the database file and applies the schema.
func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create db dir: %w", err)
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// modernc/sqlite works best with a single writer connection.
	db.SetMaxOpenConns(1)
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL;",
		"PRAGMA busy_timeout=5000;",
		"PRAGMA foreign_keys=ON;",
	} {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("pragma %q: %w", pragma, err)
		}
	}
	if _, err := db.Exec(schemaSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	s := &Store{db: db}
	if err := s.seed(); err != nil {
		db.Close()
		return nil, fmt.Errorf("seed defaults: %w", err)
	}
	return s, nil
}

// Close closes the underlying database.
func (s *Store) Close() error { return s.db.Close() }

// DB exposes the raw handle for the backup endpoints.
func (s *Store) DB() *sql.DB { return s.db }

// seed inserts default accounts, settings and the webdav_config row.
func (s *Store) seed() error {
	for _, u := range defaultUsers {
		hash, err := auth.HashPassword(u.password)
		if err != nil {
			return err
		}
		_, err = s.db.Exec(
			`INSERT INTO users (username, password, role)
			 SELECT ?, ?, ? WHERE NOT EXISTS (SELECT 1 FROM users WHERE username = ?)`,
			u.name, hash, u.role, u.name)
		if err != nil {
			return err
		}
	}
	if _, err := s.db.Exec(
		`INSERT INTO settings (key, value, description)
		 SELECT 'allow_registration', 'false', NULL
		 WHERE NOT EXISTS (SELECT 1 FROM settings WHERE key = 'allow_registration')`); err != nil {
		return err
	}
	return s.EnsureWebDAVConfig()
}
