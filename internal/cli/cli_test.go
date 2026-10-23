// SPDX-License-Identifier: GPL-3.0-or-later
// Port of synccit by James Wilson / drakeapps (https://github.com/drakeapps/synccit), GPL-3.0-or-later.

package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezhajulio/synccit-go/internal/auth"
	"github.com/rezhajulio/synccit-go/internal/store"
)

func run(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := Run(args, strings.NewReader(""), &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestCLIUserAndDeviceFlow(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "cli.db")
	t.Setenv("SYNCCIT_DB", dbPath)

	code, out, errOut := run(t, "user", "add", "alice", "--email", "alice@example.com", "--password", "alice-password")
	if code != 0 || !strings.Contains(out, "user alice created") {
		t.Fatalf("user add: code=%d out=%q err=%q", code, out, errOut)
	}
	// Duplicate fails.
	code, _, errOut = run(t, "user", "add", "alice", "--password", "alice-password")
	if code == 0 || !strings.Contains(errOut, "username already exists") {
		t.Fatalf("duplicate add: code=%d err=%q", code, errOut)
	}
	// Validation fails.
	code, _, errOut = run(t, "user", "add", "ab", "--password", "alice-password")
	if code == 0 || !strings.Contains(errOut, "at least 3 characters") {
		t.Fatalf("short username add: code=%d err=%q", code, errOut)
	}

	code, out, _ = run(t, "device", "add", "alice", "my phone")
	if code != 0 || !strings.Contains(out, "auth code for alice (my phone): ") {
		t.Fatalf("device add: code=%d out=%q", code, out)
	}
	authCode := strings.TrimSpace(out[strings.LastIndex(out, ": ")+2:])
	if len(authCode) != 6 {
		t.Fatalf("device code %q is not 6 chars", authCode)
	}
	code, out, _ = run(t, "device", "list", "alice")
	if code != 0 || !strings.Contains(out, authCode) || !strings.Contains(out, "my phone") {
		t.Fatalf("device list: code=%d out=%q", code, out)
	}

	code, out, _ = run(t, "user", "list")
	if code != 0 || !strings.Contains(out, "alice") || !strings.Contains(out, "alice@example.com") {
		t.Fatalf("user list: code=%d out=%q", code, out)
	}

	// Password change takes effect at the store level.
	code, out, _ = run(t, "user", "passwd", "alice", "--password", "new-password-1")
	if code != 0 {
		t.Fatalf("user passwd: code=%d out=%q", code, out)
	}
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	u, err := st.GetUserByUsername("alice")
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := auth.VerifyPassword("new-password-1", u.PasswordHash); !ok {
		t.Fatalf("new password does not verify")
	}
	if ok, _ := auth.VerifyPassword("alice-password", u.PasswordHash); ok {
		t.Fatalf("old password still verifies")
	}
	st.Close()

	code, out, _ = run(t, "device", "revoke", "alice", authCode)
	if code != 0 {
		t.Fatalf("device revoke: code=%d out=%q", code, out)
	}
	code, out, _ = run(t, "device", "list", "alice")
	if code != 0 || strings.Contains(out, authCode) {
		t.Fatalf("device list after revoke: code=%d out=%q", code, out)
	}

	code, out, _ = run(t, "user", "delete", "alice")
	if code != 0 || !strings.Contains(out, "user alice deleted") {
		t.Fatalf("user delete: code=%d out=%q", code, out)
	}
	code, _, errOut = run(t, "user", "delete", "alice")
	if code == 0 || !strings.Contains(errOut, "user not found") {
		t.Fatalf("second delete: code=%d err=%q", code, errOut)
	}

	code, out, _ = run(t, "help")
	if code != 0 || !strings.Contains(out, "user add") {
		t.Fatalf("help: code=%d out=%q", code, out)
	}
}
