// SPDX-License-Identifier: GPL-3.0-or-later
// Port of synccit by James Wilson / drakeapps (https://github.com/drakeapps/synccit), GPL-3.0-or-later.

package auth

import (
	"strings"
	"testing"
)

// Legacy test vector, generated with the exact original algorithm semantics
// (PBKDF2-HMAC-SHA512, 10000 iterations, 64-byte output, the base64 salt
// STRING itself fed as the PBKDF2 salt input), cross-checked with Python's
// hashlib.pbkdf2_hmac:
//
//	password = "correct horse battery staple"
//	salt raw = bytes 0..63, salt string = base64(salt raw)
const (
	legacyPassword = "correct horse battery staple"
	legacySaltB64  = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8gISIjJCUmJygpKissLS4vMDEyMzQ1Njc4OTo7PD0+Pw=="
	legacyHashB64  = "3HNFH3QN9wFnS/SoYG7erPNKNHDDe3MAH6wNrnE/EuTdtP1vj5h8NzHesw0gq+SkfcutP1g6HapkbDhZC901dA=="
	legacyStored   = "pbkdf2-sha512$10000$" + legacySaltB64 + "$" + legacyHashB64
)

func TestLegacyVectorVerify(t *testing.T) {
	ok, legacy := VerifyPassword(legacyPassword, legacyStored)
	if !ok {
		t.Fatalf("legacy vector did not verify")
	}
	if !legacy {
		t.Fatalf("legacy vector not flagged as legacy")
	}
	if ok, _ := VerifyPassword("wrong password", legacyStored); ok {
		t.Fatalf("wrong password verified against legacy hash")
	}
}

func TestLegacyHashMatchesVector(t *testing.T) {
	if got := LegacyHash(legacyPassword, legacySaltB64); got != legacyStored {
		t.Fatalf("LegacyHash mismatch:\n got: %s\nwant: %s", got, legacyStored)
	}
}

func TestArgon2idRoundTrip(t *testing.T) {
	h, err := HashPassword("s3cret-password")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=65536,t=3,p=2$") {
		t.Fatalf("unexpected PHC format: %s", h)
	}
	ok, legacy := VerifyPassword("s3cret-password", h)
	if !ok || legacy {
		t.Fatalf("argon2id verify: ok=%v legacy=%v", ok, legacy)
	}
	if ok, _ := VerifyPassword("nope-nope", h); ok {
		t.Fatalf("wrong password verified against argon2id hash")
	}
	// Salts are random: two hashes of the same password differ.
	h2, _ := HashPassword("s3cret-password")
	if h == h2 {
		t.Fatalf("two argon2id hashes are identical; salt is not random")
	}
}

func TestGenerateAuthCodeFormat(t *testing.T) {
	for i := 0; i < 50; i++ {
		code, err := GenerateAuthCode()
		if err != nil {
			t.Fatal(err)
		}
		if len(code) != 6 {
			t.Fatalf("code length = %d, want 6 (%q)", len(code), code)
		}
		for _, c := range code {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9') {
				t.Fatalf("code %q contains out-of-alphabet char %q", code, c)
			}
		}
	}
}

func TestGenerateLoginTokenFormat(t *testing.T) {
	tok, err := GenerateLoginToken()
	if err != nil {
		t.Fatal(err)
	}
	if len(tok) != 64 {
		t.Fatalf("token length = %d, want 64", len(tok))
	}
	tok2, _ := GenerateLoginToken()
	if tok == tok2 {
		t.Fatalf("two login tokens are identical")
	}
}

func TestValidateAccount(t *testing.T) {
	cases := []struct {
		username, password, email, want string
	}{
		{"james", "secret1", "james@example.com", ""},
		{"james", "secret1", "", ""},
		{"james", "secret1", "not-an-email", "email not valid"},
		{"ab", "secret1", "", "username needs to be at least 3 characters long"},
		{"james", "short", "", "password needs to be at least 6 characters long"},
		{"bad name", "secret1", "", "username must consist of letters, numbers, or underscores"},
		{"bad-name", "secret1", "", "username must consist of letters, numbers, or underscores"},
	}
	for _, c := range cases {
		if got := ValidateAccount(c.username, c.password, c.email); got != c.want {
			t.Errorf("ValidateAccount(%q,%q,%q) = %q, want %q", c.username, c.password, c.email, got, c.want)
		}
	}
}
