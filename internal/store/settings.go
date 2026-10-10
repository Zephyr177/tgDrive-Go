package store

import (
	"database/sql"
	"errors"
	"fmt"
)

// Setting is one row of the settings key-value table.
type Setting struct {
	ID          int64  `json:"id"`
	Key         string `json:"key"`
	Value       string `json:"value"`
	Description string `json:"description"`
}

// GetSetting returns a setting value; sql.ErrNoRows when absent.
func (s *Store) GetSetting(key string) (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", sql.ErrNoRows
	}
	if err != nil {
		return "", fmt.Errorf("get setting %q: %w", key, err)
	}
	return v, nil
}

// UpdateSetting updates an existing setting (upstream semantics: no create).
func (s *Store) UpdateSetting(key, value string) error {
	res, err := s.db.Exec(`UPDATE settings SET value = ? WHERE key = ?`, value, key)
	if err != nil {
		return fmt.Errorf("update setting %q: %w", key, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// ListSettings returns all settings rows.
func (s *Store) ListSettings() ([]Setting, error) {
	rows, err := s.db.Query(`SELECT id, key, value, COALESCE(description, '') FROM settings ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list settings: %w", err)
	}
	defer rows.Close()
	var out []Setting
	for rows.Next() {
		var st Setting
		if err := rows.Scan(&st.ID, &st.Key, &st.Value, &st.Description); err != nil {
			return nil, fmt.Errorf("scan setting: %w", err)
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// BotConfig is one row of the configs table (Telegram bot credentials).
type BotConfig struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Token  string `json:"token"`
	Target string `json:"target"`
	URL    string `json:"url"`
	Pass   string `json:"pass"`
}

const botConfigColumns = `id, name, token, COALESCE(target, ''), COALESCE(url, ''), COALESCE(pass, '')`

func scanBotConfig(row interface{ Scan(...any) error }) (*BotConfig, error) {
	var c BotConfig
	if err := row.Scan(&c.ID, &c.Name, &c.Token, &c.Target, &c.URL, &c.Pass); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, sql.ErrNoRows
		}
		return nil, fmt.Errorf("scan bot config: %w", err)
	}
	return &c, nil
}

// ListBotConfigs returns all bot configs ordered by id.
func (s *Store) ListBotConfigs() ([]BotConfig, error) {
	rows, err := s.db.Query(`SELECT ` + botConfigColumns + ` FROM configs ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list bot configs: %w", err)
	}
	defer rows.Close()
	var out []BotConfig
	for rows.Next() {
		c, err := scanBotConfig(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// BotConfigByName returns the newest config row with the given name.
func (s *Store) BotConfigByName(name string) (*BotConfig, error) {
	return scanBotConfig(s.db.QueryRow(
		`SELECT `+botConfigColumns+` FROM configs WHERE name = ? ORDER BY id DESC LIMIT 1`, name))
}

// UpsertBotConfig inserts or updates a config by name (upstream uses delete+insert).
func (s *Store) UpsertBotConfig(c *BotConfig) error {
	res, err := s.db.Exec(
		`UPDATE configs SET token = ?, target = ?, url = ?, pass = ? WHERE name = ?`,
		c.Token, c.Target, c.URL, c.Pass, c.Name)
	if err != nil {
		return fmt.Errorf("update bot config: %w", err)
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return nil
	}
	_, err = s.db.Exec(
		`INSERT INTO configs (name, token, target, url, pass) VALUES (?, ?, ?, ?, ?)`,
		c.Name, c.Token, c.Target, c.URL, c.Pass)
	if err != nil {
		return fmt.Errorf("insert bot config: %w", err)
	}
	return nil
}

// DeleteBotConfigByName removes all config rows with the given name.
func (s *Store) DeleteBotConfigByName(name string) error {
	if _, err := s.db.Exec(`DELETE FROM configs WHERE name = ?`, name); err != nil {
		return fmt.Errorf("delete bot config: %w", err)
	}
	return nil
}
