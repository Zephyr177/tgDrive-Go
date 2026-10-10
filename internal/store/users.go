package store

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/Zephyr177/tgDrive-Go/internal/model"
)

const userColumns = `id, username, password, role, COALESCE(last_login_time, ''), COALESCE(email, '')`

func scanUser(row interface{ Scan(...any) error }) (*model.User, error) {
	var u model.User
	if err := row.Scan(&u.ID, &u.Username, &u.Password, &u.Role, &u.LastLoginTime, &u.Email); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, sql.ErrNoRows
		}
		return nil, fmt.Errorf("scan user: %w", err)
	}
	return &u, nil
}

// CreateUser inserts a user and returns its id.
func (s *Store) CreateUser(username, password, role, email string) (int64, error) {
	res, err := s.db.Exec(
		`INSERT INTO users (username, password, role, email) VALUES (?, ?, ?, ?)`,
		username, password, role, email)
	if err != nil {
		return 0, fmt.Errorf("create user: %w", err)
	}
	return res.LastInsertId()
}

// UserByUsername returns the user with the given name (sql.ErrNoRows if none).
func (s *Store) UserByUsername(username string) (*model.User, error) {
	return scanUser(s.db.QueryRow(`SELECT `+userColumns+` FROM users WHERE username = ?`, username))
}

// UserByUsernameOrEmail supports the upstream "username or email" login.
func (s *Store) UserByUsernameOrEmail(nameOrEmail string) (*model.User, error) {
	return scanUser(s.db.QueryRow(
		`SELECT `+userColumns+` FROM users WHERE username = ? OR email = ?`, nameOrEmail, nameOrEmail))
}

// UserByEmail returns the user with the given email (sql.ErrNoRows if none).
func (s *Store) UserByEmail(email string) (*model.User, error) {
	return scanUser(s.db.QueryRow(`SELECT `+userColumns+` FROM users WHERE email = ?`, email))
}

// UserByID returns the user with the given id (sql.ErrNoRows if none).
func (s *Store) UserByID(id int64) (*model.User, error) {
	return scanUser(s.db.QueryRow(`SELECT `+userColumns+` FROM users WHERE id = ?`, id))
}

// ListUsers returns all users ordered by id descending (upstream order).
func (s *Store) ListUsers() ([]model.User, error) {
	rows, err := s.db.Query(`SELECT ` + userColumns + ` FROM users ORDER BY id DESC`)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()
	var out []model.User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *u)
	}
	return out, rows.Err()
}

// UpdateUserPassword replaces the password hash of a user.
func (s *Store) UpdateUserPassword(id int64, password string) error {
	_, err := s.db.Exec(`UPDATE users SET password = ? WHERE id = ?`, password, id)
	if err != nil {
		return fmt.Errorf("update password: %w", err)
	}
	return nil
}

// UpdateLastLoginTime stores the upstream-formatted "yyyy-MM-dd HH:mm:ss" string.
func (s *Store) UpdateLastLoginTime(id int64, ts string) error {
	_, err := s.db.Exec(`UPDATE users SET last_login_time = ? WHERE id = ?`, ts, id)
	if err != nil {
		return fmt.Errorf("update last login time: %w", err)
	}
	return nil
}

// UpdateUserRole replaces the role of a user.
func (s *Store) UpdateUserRole(id int64, role string) error {
	if _, err := s.db.Exec(`UPDATE users SET role = ? WHERE id = ?`, role, id); err != nil {
		return fmt.Errorf("update role: %w", err)
	}
	return nil
}

// DeleteUser removes a user by id.
func (s *Store) DeleteUser(id int64) error {
	if _, err := s.db.Exec(`DELETE FROM users WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete user: %w", err)
	}
	return nil
}
