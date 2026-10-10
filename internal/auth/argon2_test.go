package auth

import "testing"

// Hashes copied from upstream Flyway V7__CleanupUserSystem.sql: Spring Security
// Argon2id with defaultsForSpringSecurity_v5_8 (m=16384,t=2,p=1).
const (
	upstreamAdminHash   = "$argon2id$v=19$m=16384,t=2,p=1$PR7R68i1IaECQu3O5b6meg$YwySH08w432Za4Igj+zNcAaJZ2D1Qs5UgfROIXb/+u4"
	upstreamVisitorHash = "$argon2id$v=19$m=16384,t=2,p=1$6yZI0SIgjFCjqjChnnPYRQ$KYy5FZmZnMxCGjGVWWzd6DB8g8DczIUrOlANXaFnyts"
	legacyMD5Hash       = "e10adc3949ba59abbe56e057f20f883e" // md5("123456") from upstream schema.sql
)

func TestCheckPasswordUpstreamArgon2Hashes(t *testing.T) {
	if !CheckPassword(upstreamAdminHash, "123456") {
		t.Error("admin hash should verify password 123456")
	}
	if CheckPassword(upstreamAdminHash, "wrong") {
		t.Error("admin hash must reject a wrong password")
	}
	if !CheckPassword(upstreamVisitorHash, "hello") {
		t.Error("visitor hash should verify password hello")
	}
	if CheckPassword(upstreamVisitorHash, "123456") {
		t.Error("visitor hash must reject the admin password")
	}
}

func TestCheckPasswordLegacyMD5(t *testing.T) {
	if !CheckPassword(legacyMD5Hash, "123456") {
		t.Error("legacy md5 hash should still verify")
	}
	if CheckPassword(legacyMD5Hash, "654321") {
		t.Error("legacy md5 hash must reject a wrong password")
	}
}

func TestHashPasswordRoundTrip(t *testing.T) {
	hash, err := HashPassword("secret123")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !CheckPassword(hash, "secret123") {
		t.Error("freshly hashed password should verify")
	}
	if CheckPassword(hash, "secret124") {
		t.Error("freshly hashed password must reject a different password")
	}
}

func TestSessionManager(t *testing.T) {
	m := NewManager(0) // immediately expired sessions
	token, err := m.Create(42)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if id, ok := m.UserID(token); ok || id != 0 {
		t.Fatal("expired token must not resolve")
	}
	m2 := NewManager(60 * 1e9)
	token2, _ := m2.Create(7)
	if id, ok := m2.UserID(token2); !ok || id != 7 {
		t.Fatalf("token should resolve to user 7, got %d ok=%v", id, ok)
	}
	m2.Revoke(token2)
	if _, ok := m2.UserID(token2); ok {
		t.Fatal("revoked token must not resolve")
	}
}
