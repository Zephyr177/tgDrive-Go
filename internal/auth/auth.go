// Package auth implements sa-token style sessions and password hashing.
package auth

import (
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"strings"
	"sync"
	"time"
)

// Manager stores active sessions in memory, mirroring the upstream sa-token setup.
type Manager struct {
	mu     sync.Mutex
	ttl    time.Duration
	tokens map[string]session
}

type session struct {
	userID    int64
	expiresAt time.Time
}

// NewManager returns a session manager with the given token lifetime.
func NewManager(ttl time.Duration) *Manager {
	return &Manager{ttl: ttl, tokens: map[string]session{}}
}

// Create issues a new random token (uuid style, like sa-token) for the user.
func (m *Manager) Create(userID int64) (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := hex.EncodeToString(b)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tokens[token] = session{userID: userID, expiresAt: time.Now().Add(m.ttl)}
	return token, nil
}

// UserID resolves a token to a user id; ok is false when unknown or expired.
func (m *Manager) UserID(token string) (int64, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.tokens[token]
	if !ok {
		return 0, false
	}
	if !time.Now().Before(s.expiresAt) {
		delete(m.tokens, token)
		return 0, false
	}
	return s.userID, true
}

// TTL returns the configured token lifetime (used for expireAt computation).
func (m *Manager) TTL() time.Duration { return m.ttl }

// Revoke deletes a token (logout).
func (m *Manager) Revoke(token string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.tokens, token)
}

// CheckPassword verifies a password against a stored hash. Spring Security
// Argon2id hashes are the primary format; legacy md5 hex hashes from the
// upstream schema.sql bootstrap are still accepted.
func CheckPassword(hash, password string) bool {
	if strings.HasPrefix(hash, "$argon2") {
		ok, err := verifyArgon2id(hash, password)
		return err == nil && ok
	}
	if isMD5Hex(hash) {
		sum := md5.Sum([]byte(password))
		return strings.EqualFold(hash, hex.EncodeToString(sum[:]))
	}
	return false
}

func isMD5Hex(s string) bool {
	if len(s) != 32 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}
