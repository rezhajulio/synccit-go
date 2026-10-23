// SPDX-License-Identifier: GPL-3.0-or-later
// Port of synccit by James Wilson / drakeapps (https://github.com/drakeapps/synccit), GPL-3.0-or-later.

// Package cli implements the synccit command line: the HTTP server (serve,
// the default) plus user and device administration subcommands. Registration
// on a fresh instance is done here, not over the API.
package cli

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/rezhajulio/synccit-go/internal/api"
	"github.com/rezhajulio/synccit-go/internal/auth"
	"github.com/rezhajulio/synccit-go/internal/config"
	"github.com/rezhajulio/synccit-go/internal/store"
)

const usageText = `synccit-go — Go + SQLite port of synccit (syncs your reddit history between devices)

Usage:
  synccit [serve] [--db <path>] [--addr <addr>] [--allow-registration]
  synccit user add <username> [--email <email>] [--password <pw>]
  synccit user list
  synccit user passwd <username> [--password <pw>]
  synccit user delete <username>
  synccit device add <username> "<device name>"
  synccit device list <username>
  synccit device revoke <username> <code>
  synccit help

Configuration (environment, flags override on serve):
  SYNCCIT_DB                   SQLite database path (default ./synccit.db)
  SYNCCIT_ADDR                 Listen address (default :8080)
  SYNCCIT_ALLOW_REGISTRATION   "true"/"1" enables API account creation
                               (default: disabled; create users via the CLI)

Examples:
  synccit user add james --email james@example.com
  synccit device add james "my phone"
  synccit serve --addr :8080
  # then point your synccit client at https://your-host/api.php
`

// Run executes one CLI invocation and returns the process exit code.
func Run(args []string, in io.Reader, out, errOut io.Writer) int {
	if len(args) == 0 {
		return runServe(nil, out, errOut)
	}
	switch args[0] {
	case "serve":
		return runServe(args[1:], out, errOut)
	case "user":
		return runUser(args[1:], in, out, errOut)
	case "device":
		return runDevice(args[1:], out, errOut)
	case "help", "--help", "-h":
		fmt.Fprint(out, usageText)
		return 0
	default:
		fmt.Fprintf(errOut, "unknown command: %s\n\n%s", args[0], usageText)
		return 2
	}
}

// ---- serve ----

func runServe(args []string, out, errOut io.Writer) int {
	cfg := config.FromEnv()
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(errOut)
	dbPath := fs.String("db", cfg.DBPath, "SQLite database path")
	addr := fs.String("addr", cfg.Addr, "listen address")
	allowReg := fs.Bool("allow-registration", cfg.AllowRegistration, "allow API account creation")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg.DBPath = *dbPath
	cfg.Addr = *addr
	cfg.AllowRegistration = *allowReg

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		fmt.Fprintf(errOut, "open database: %v\n", err)
		return 1
	}
	defer st.Close()

	server := &http.Server{
		Addr:              cfg.Addr,
		Handler:           api.NewServer(st, cfg.AllowRegistration).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	fmt.Fprintf(errOut, "synccit listening on %s (db %s, registration %s)\n",
		cfg.Addr, cfg.DBPath, map[bool]string{true: "open", false: "locked (CLI only)"}[cfg.AllowRegistration])
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintf(errOut, "serve: %v\n", err)
		return 1
	}
	return 0
}

// ---- flag helpers ----

// splitArgs separates --flag value / --flag=value / boolean --flag arguments
// from positional arguments, so subcommand flags may appear anywhere.
func splitArgs(args []string, valueFlags map[string]*string, boolFlags map[string]*bool) []string {
	var positionals []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "--") {
			positionals = append(positionals, a)
			continue
		}
		name := strings.TrimPrefix(a, "--")
		if eq := strings.Index(name, "="); eq >= 0 {
			key, val := name[:eq], name[eq+1:]
			if dst, ok := valueFlags[key]; ok {
				*dst = val
			} else if dst, ok := boolFlags[key]; ok {
				*dst = val == "true" || val == "1"
			}
			continue
		}
		if dst, ok := valueFlags[name]; ok {
			if i+1 < len(args) {
				i++
				*dst = args[i]
			}
			continue
		}
		if dst, ok := boolFlags[name]; ok {
			*dst = true
			continue
		}
		positionals = append(positionals, a)
	}
	return positionals
}

func openStore(errOut io.Writer) *store.Store {
	cfg := config.FromEnv()
	if v := os.Getenv("SYNCCIT_DB"); v != "" {
		cfg.DBPath = v
	}
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		fmt.Fprintf(errOut, "open database: %v\n", err)
		return nil
	}
	return st
}

// promptPassword returns the --password flag value when set; otherwise it
// prompts. On a terminal it uses term.ReadPassword (with a confirmation
// prompt when confirm is true); on a pipe it reads one line from in.
func promptPassword(in io.Reader, out io.Writer, flagValue string, confirm bool) (string, error) {
	if flagValue != "" {
		return flagValue, nil
	}
	if f, ok := in.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		fmt.Fprint(out, "Password: ")
		pw, err := term.ReadPassword(int(f.Fd()))
		fmt.Fprintln(out)
		if err != nil {
			return "", err
		}
		if confirm {
			fmt.Fprint(out, "Confirm password: ")
			pw2, err := term.ReadPassword(int(f.Fd()))
			fmt.Fprintln(out)
			if err != nil {
				return "", err
			}
			if string(pw) != string(pw2) {
				return "", errors.New("passwords do not match")
			}
		}
		return string(pw), nil
	}
	fmt.Fprint(out, "Password: ")
	reader := bufio.NewReader(in)
	line, err := reader.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// ---- user ----

func runUser(args []string, in io.Reader, out, errOut io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(errOut, usageText)
		return 2
	}
	sub := args[0]
	rest := args[1:]

	switch sub {
	case "add":
		var email, password string
		pos := splitArgs(rest,
			map[string]*string{"email": &email, "password": &password},
			nil)
		if len(pos) < 1 {
			fmt.Fprintln(errOut, "usage: synccit user add <username> [--email <email>] [--password <pw>]")
			return 2
		}
		username := pos[0]
		pw, err := promptPassword(in, errOut, password, true)
		if err != nil {
			fmt.Fprintf(errOut, "password: %v\n", err)
			return 1
		}
		if msg := auth.ValidateAccount(username, pw, email); msg != "" {
			fmt.Fprintln(errOut, msg)
			return 1
		}
		st := openStore(errOut)
		if st == nil {
			return 1
		}
		defer st.Close()
		if _, err := st.GetUserByUsername(username); err == nil {
			fmt.Fprintln(errOut, "username already exists")
			return 1
		}
		hash, err := auth.HashPassword(pw)
		if err != nil {
			fmt.Fprintf(errOut, "hash password: %v\n", err)
			return 1
		}
		id, err := st.CreateUser(username, hash, email, time.Now().Unix(), "cli", "")
		if err != nil {
			if store.IsUniqueViolation(err) {
				fmt.Fprintln(errOut, "username already exists")
			} else {
				fmt.Fprintf(errOut, "create user: %v\n", err)
			}
			return 1
		}
		fmt.Fprintf(out, "user %s created (id %d)\n", username, id)
		return 0

	case "list":
		st := openStore(errOut)
		if st == nil {
			return 1
		}
		defer st.Close()
		users, err := st.ListUsers()
		if err != nil {
			fmt.Fprintf(errOut, "list users: %v\n", err)
			return 1
		}
		for _, u := range users {
			fmt.Fprintf(out, "%d\t%s\t%s\t%s\n", u.ID, u.Username, u.Email,
				time.Unix(u.Created, 0).UTC().Format("2006-01-02 15:04:05"))
		}
		return 0

	case "passwd":
		var password string
		pos := splitArgs(rest, map[string]*string{"password": &password}, nil)
		if len(pos) < 1 {
			fmt.Fprintln(errOut, "usage: synccit user passwd <username> [--password <pw>]")
			return 2
		}
		username := pos[0]
		st := openStore(errOut)
		if st == nil {
			return 1
		}
		defer st.Close()
		u, err := st.GetUserByUsername(username)
		if errors.Is(err, store.ErrNotFound) {
			fmt.Fprintln(errOut, "user not found")
			return 1
		}
		if err != nil {
			fmt.Fprintf(errOut, "lookup user: %v\n", err)
			return 1
		}
		pw, err := promptPassword(in, errOut, password, true)
		if err != nil {
			fmt.Fprintf(errOut, "password: %v\n", err)
			return 1
		}
		if msg := auth.ValidatePassword(pw); msg != "" {
			fmt.Fprintln(errOut, msg)
			return 1
		}
		hash, err := auth.HashPassword(pw)
		if err != nil {
			fmt.Fprintf(errOut, "hash password: %v\n", err)
			return 1
		}
		if err := st.UpdatePasswordHash(u.ID, hash); err != nil {
			fmt.Fprintf(errOut, "update password: %v\n", err)
			return 1
		}
		fmt.Fprintf(out, "password updated for %s\n", username)
		return 0

	case "delete":
		if len(rest) < 1 {
			fmt.Fprintln(errOut, "usage: synccit user delete <username>")
			return 2
		}
		username := rest[0]
		st := openStore(errOut)
		if st == nil {
			return 1
		}
		defer st.Close()
		u, err := st.GetUserByUsername(username)
		if errors.Is(err, store.ErrNotFound) {
			fmt.Fprintln(errOut, "user not found")
			return 1
		}
		if err != nil {
			fmt.Fprintf(errOut, "lookup user: %v\n", err)
			return 1
		}
		if err := st.DeleteUserCascade(u.ID); err != nil {
			fmt.Fprintf(errOut, "delete user: %v\n", err)
			return 1
		}
		fmt.Fprintf(out, "user %s deleted\n", username)
		return 0
	}
	fmt.Fprintf(errOut, "unknown user subcommand: %s\n", sub)
	return 2
}

// ---- device ----

func runDevice(args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(errOut, usageText)
		return 2
	}
	sub := args[0]
	rest := args[1:]

	switch sub {
	case "add":
		if len(rest) < 2 {
			fmt.Fprintln(errOut, `usage: synccit device add <username> "<device name>"`)
			return 2
		}
		username := rest[0]
		deviceName := strings.Join(rest[1:], " ")
		st := openStore(errOut)
		if st == nil {
			return 1
		}
		defer st.Close()
		u, err := st.GetUserByUsername(username)
		if errors.Is(err, store.ErrNotFound) {
			fmt.Fprintln(errOut, "user not found")
			return 1
		}
		if err != nil {
			fmt.Fprintf(errOut, "lookup user: %v\n", err)
			return 1
		}
		code, err := auth.GenerateAuthCode()
		if err != nil {
			fmt.Fprintf(errOut, "generate code: %v\n", err)
			return 1
		}
		if err := st.InsertAuthCode(u.ID, u.Username, code, deviceName, time.Now().Unix(), "cli"); err != nil {
			fmt.Fprintf(errOut, "add device: %v\n", err)
			return 1
		}
		fmt.Fprintf(out, "auth code for %s (%s): %s\n", username, deviceName, code)
		return 0

	case "list":
		if len(rest) < 1 {
			fmt.Fprintln(errOut, "usage: synccit device list <username>")
			return 2
		}
		username := rest[0]
		st := openStore(errOut)
		if st == nil {
			return 1
		}
		defer st.Close()
		if _, err := st.GetUserByUsername(username); errors.Is(err, store.ErrNotFound) {
			fmt.Fprintln(errOut, "user not found")
			return 1
		}
		codes, err := st.ListAuthCodesByUsername(username)
		if err != nil {
			fmt.Fprintf(errOut, "list devices: %v\n", err)
			return 1
		}
		for _, c := range codes {
			fmt.Fprintf(out, "%s\t%s\t%s\t%s\n", c.AuthHash, c.Description,
				time.Unix(c.Created, 0).UTC().Format("2006-01-02 15:04:05"), c.CreatedBy)
		}
		return 0

	case "revoke":
		if len(rest) < 2 {
			fmt.Fprintln(errOut, "usage: synccit device revoke <username> <code>")
			return 2
		}
		username, code := rest[0], rest[1]
		st := openStore(errOut)
		if st == nil {
			return 1
		}
		defer st.Close()
		u, err := st.GetUserByUsername(username)
		if errors.Is(err, store.ErrNotFound) {
			fmt.Fprintln(errOut, "user not found")
			return 1
		}
		if err != nil {
			fmt.Fprintf(errOut, "lookup user: %v\n", err)
			return 1
		}
		n, err := st.DeleteAuthCode(u.ID, u.Username, code)
		if err != nil {
			fmt.Fprintf(errOut, "revoke device: %v\n", err)
			return 1
		}
		if n == 0 {
			fmt.Fprintln(errOut, "auth code not found")
			return 1
		}
		fmt.Fprintf(out, "auth code %s for %s revoked\n", code, username)
		return 0
	}
	fmt.Fprintf(errOut, "unknown device subcommand: %s\n", sub)
	return 2
}
