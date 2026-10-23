// SPDX-License-Identifier: GPL-3.0-or-later
// Port of synccit by James Wilson / drakeapps (https://github.com/drakeapps/synccit), GPL-3.0-or-later.

// Package auth handles password hashing and verification (argon2id for new
// hashes, legacy PBKDF2-HMAC-SHA512 for rows imported from the PHP version),
// plus generation of device auth codes and account login tokens.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"crypto/sha512"
	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/pbkdf2"
)

// Argon2id parameters for new password hashes.
const (
	argonMemoryKiB = 65536
	argonTime      = 3
	argonThreads   = 2
	argonSaltLen   = 16
	argonKeyLen    = 32
)

// Legacy PBKDF2 parameters, matching the original pbkdf2.php:
// PBKDF2-HMAC-SHA512, 10000 iterations, 64-byte output.
const (
	legacyPrefix     = "pbkdf2-sha512"
	legacyIterations = 10000
	legacyKeyLen     = 64
)

// HashPassword returns a PHC-encoded argon2id hash of password:
//
//	$argon2id$v=19$m=65536,t=3,p=2$<std-b64 salt>$<std-b64 hash>
func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemoryKiB, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		argonMemoryKiB, argonTime, argonThreads,
		base64.StdEncoding.EncodeToString(salt),
		base64.StdEncoding.EncodeToString(key)), nil
}

// VerifyPassword checks password against a stored hash. It reports whether the
// password matched, and whether the stored hash uses the legacy PBKDF2 scheme
// (in which case the caller should transparently rehash to argon2id on a
// successful match).
func VerifyPassword(password, stored string) (ok bool, legacy bool) {
	if strings.HasPrefix(stored, "$argon2id$") {
		return verifyArgon2id(password, stored), false
	}
	if strings.HasPrefix(stored, legacyPrefix+"$") {
		return verifyLegacyPBKDF2(password, stored), true
	}
	if strings.HasPrefix(stored, "sha512:") {
		// Original on-disk split form, reassembled: sha512:iter:salt:hash.
		parts := strings.Split(stored, ":")
		if len(parts) != 4 {
			return false, true
		}
		converted := legacyPrefix + "$" + parts[1] + "$" + parts[2] + "$" + parts[3]
		return verifyLegacyPBKDF2(password, converted), true
	}
	return false, false
}

func verifyArgon2id(password, stored string) bool {
	// $argon2id$v=19$m=...,t=...,p=...$salt$hash
	parts := strings.Split(stored, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var mem, t uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &mem, &t, &threads); err != nil {
		return false
	}
	salt, err := base64.StdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := base64.StdEncoding.DecodeString(parts[5])
	if err != nil || len(want) == 0 {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, t, mem, threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// verifyLegacyPBKDF2 verifies the format
//
//	pbkdf2-sha512$<iterations>$<salt-b64-string>$<hash-b64>
//
// Critical PHP compatibility note: the stored salt is the base64 string
// itself (pieces[2] of the original "sha512:10000:salt:hash" hashset), and
// the original validate_password feeds that base64 string — not its decoded
// bytes — as the PBKDF2 salt input. The stored hash is base64 of the raw
// 64-byte PBKDF2 output.
func verifyLegacyPBKDF2(password, stored string) bool {
	parts := strings.Split(stored, "$")
	if len(parts) != 4 || parts[0] != legacyPrefix {
		return false
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil || iter <= 0 {
		return false
	}
	want, err := base64.StdEncoding.DecodeString(parts[3])
	if err != nil || len(want) == 0 {
		return false
	}
	got := pbkdf2.Key([]byte(password), []byte(parts[2]), iter, len(want), sha512.New)
	return subtle.ConstantTimeCompare(got, want) == 1
}

// LegacyHash builds a stored-format legacy PBKDF2 hash from a password and a
// base64 salt string, using the exact original algorithm semantics. It exists
// so tests (and imports) can construct legacy rows.
func LegacyHash(password, saltB64 string) string {
	key := pbkdf2.Key([]byte(password), []byte(saltB64), legacyIterations, legacyKeyLen, sha512.New)
	return fmt.Sprintf("%s$%d$%s$%s", legacyPrefix, legacyIterations, saltB64,
		base64.StdEncoding.EncodeToString(key))
}

// IsLegacy reports whether a stored hash uses a legacy scheme.
func IsLegacy(stored string) bool {
	return strings.HasPrefix(stored, legacyPrefix+"$") || strings.HasPrefix(stored, "sha512:")
}

const authCodeAlphabet = "abcdefghijklmnopqrstuvwxyz0123456789"

// GenerateAuthCode returns a 6-character device auth code drawn from
// [a-z0-9] using crypto/rand, replacing the original genrand() which used
// PHP's weak rand(). Format is unchanged for client compatibility.
func GenerateAuthCode() (string, error) {
	var b strings.Builder
	b.Grow(6)
	max := big.NewInt(int64(len(authCodeAlphabet)))
	for i := 0; i < 6; i++ {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		b.WriteByte(authCodeAlphabet[n.Int64()])
	}
	return b.String(), nil
}

// GenerateLoginToken returns a 64-character hex login token: the SHA-256 of
// 32 crypto/rand bytes. (The original used sha256(uniqid()).)
func GenerateLoginToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	sum := sha256.Sum256(buf)
	return hex.EncodeToString(sum[:]), nil
}
