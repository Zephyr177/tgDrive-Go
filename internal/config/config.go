// Package config holds process-wide settings, overridable via TGDRIVE_* env vars.
package config

import (
	"os"
	"strconv"
	"time"
)

// Config mirrors the upstream application.yml defaults.
type Config struct {
	Port         int           // server.port
	DBPath       string        // datasource url (sqlite file)
	UploadDir    string        // app.upload.path
	TokenName    string        // sa-token token-name (transport header/cookie)
	TokenTimeout time.Duration // sa-token timeout
	BotAPIURL    string        // custom Telegram Bot API base URL ("" = official)
}

// Load returns the configuration with defaults, then env overrides applied.
func Load() Config {
	c := Config{
		Port:         8085,
		DBPath:       "db/tgDrive.db",
		UploadDir:    "uploads",
		TokenName:    "tgdrive",
		TokenTimeout: 10000 * time.Hour, // 36000000s
		BotAPIURL:    "",
	}
	if v := os.Getenv("TGDRIVE_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			c.Port = p
		}
	}
	if v := os.Getenv("TGDRIVE_DB"); v != "" {
		c.DBPath = v
	}
	if v := os.Getenv("TGDRIVE_UPLOAD_DIR"); v != "" {
		c.UploadDir = v
	}
	if v := os.Getenv("TGDRIVE_BOT_API_URL"); v != "" {
		c.BotAPIURL = v
	}
	return c
}
