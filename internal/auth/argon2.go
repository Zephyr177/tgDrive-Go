package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters matching Spring Security's defaultsForSpringSecurity_v5_8,
// so hashes are interchangeable with the upstream Java implementation.
const (
	argonMemory      = 16 * 1024 // KiB
	argonIterations  = 2
	argonParallelism = 1
	argonSaltLen     = 16
	argonKeyLen      = 32
)

// HashPassword returns a Spring-compatible Argon2id hash:
// $argon2id$v=19$m=16384,t=2,p=1$<b64salt>$<b64hash> (unpadded base64).
func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, argonIterations, argonMemory, argonParallelism, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		argonMemory, argonIterations, argonParallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

var errMalformedHash = errors.New("auth: malformed argon2 hash")

// verifyArgon2id checks a password against a PHC-format argon2id hash.
func verifyArgon2id(hash, password string) (bool, error) {
	parts := strings.Split(hash, "$")
	// ["", "argon2id", "v=19", "m=16384,t=2,p=1", salt, key]
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, errMalformedHash
	}
	var m, t, p uint32
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false, errMalformedHash
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, errMalformedHash
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, errMalformedHash
	}
	got := argon2.IDKey([]byte(password), salt, t, m, uint8(p), uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}
