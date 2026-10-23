// SPDX-License-Identifier: GPL-3.0-or-later
// Port of synccit by James Wilson / drakeapps (https://github.com/drakeapps/synccit), GPL-3.0-or-later.

// Package store is a hand-written database/sql layer over SQLite. It owns the
// schema (applied idempotently at startup) and every query the API and CLI
// need.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	_ "modernc.org/sqlite"
)

// Store wraps the SQLite database handle.
type Store struct {
	DB *sql.DB
}

// User is a row of the `user` table. Nullable columns are coalesced to "".
type User struct {
	ID            int64
	Username      string
	PasswordHash  string
	Email         string
	LastLogin     int64
	LastActivity  int64
	LastIP        string
	LoginAttempts int64
	Created       int64
	CreatedBy     string
	ResetHash     string
	CanReset      int64
}

// AuthCode is a row of the `authcodes` table.
type AuthCode struct {
	ID          int64
	UserID      int64
	Username    string
	AuthHash    string
	Description string
	LastUsed    int64
	Created     int64
	CreatedBy   string
}

// Link is a row of the `links` table.
type Link struct {
	ID               int64
	LinkID           string
	UserID           int64
	LastVisit        int64
	LastCommentTime  int64
	LastCommentCount int64
	FirstVisit       int64
	LastCall         string
	Developers       string
}

// App is a row of the `apps` table.
type App struct {
	Name        string
	Description string
	Link        string
	Platform    string
}

// LinkUpdate is one parsed link update from an API `update` call.
type LinkUpdate struct {
	LinkID       string
	HasComment   bool
	CommentCount int64
	MarkLink     bool
}

var schemaStatements = []string{
	`CREATE TABLE IF NOT EXISTS user (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		username TEXT UNIQUE NOT NULL,
		password_hash TEXT NOT NULL,
		email TEXT,
		lastlogin INTEGER DEFAULT 0,
		lastactivity INTEGER DEFAULT 0,
		lastip TEXT,
		loginattempts INTEGER DEFAULT 0,
		created INTEGER NOT NULL DEFAULT 0,
		createdby TEXT,
		resethash TEXT,
		canreset INTEGER DEFAULT 0
	)`,
	`CREATE TABLE IF NOT EXISTS authcodes (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		userid INTEGER NOT NULL,
		username TEXT NOT NULL,
		authhash TEXT NOT NULL,
		description TEXT NOT NULL,
		lastused INTEGER DEFAULT 0,
		created INTEGER DEFAULT 0,
		createdby TEXT
	)`,
	`CREATE INDEX IF NOT EXISTS idx_authcodes_authhash ON authcodes(authhash)`,
	`CREATE INDEX IF NOT EXISTS idx_authcodes_userid ON authcodes(userid)`,
	`CREATE TABLE IF NOT EXISTS links (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		linkid TEXT NOT NULL,
		userid INTEGER NOT NULL,
		lastvisit INTEGER NOT NULL DEFAULT 0,
		lastcommenttime INTEGER NOT NULL DEFAULT 0,
		lastcommentcount INTEGER NOT NULL DEFAULT 0,
		firstvisit INTEGER NOT NULL DEFAULT 0,
		lastcall TEXT NOT NULL DEFAULT '',
		developers TEXT NOT NULL DEFAULT '',
		UNIQUE(linkid, userid)
	)`,
	`CREATE INDEX IF NOT EXISTS idx_links_userid ON links(userid)`,
	`CREATE INDEX IF NOT EXISTS idx_links_userid_lastvisit ON links(userid, lastvisit)`,
	`CREATE INDEX IF NOT EXISTS idx_links_userid_lastcommenttime ON links(userid, lastcommenttime)`,
	`CREATE TABLE IF NOT EXISTS logincodes (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		userid INTEGER NOT NULL,
		authhash TEXT NOT NULL,
		lastlogin INTEGER NOT NULL DEFAULT 0,
		created INTEGER NOT NULL DEFAULT 0
	)`,
	`CREATE INDEX IF NOT EXISTS idx_logincodes_authhash ON logincodes(authhash)`,
	`CREATE TABLE IF NOT EXISTS faq (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		question TEXT NOT NULL,
		answer TEXT NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS apps (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT UNIQUE NOT NULL,
		description TEXT,
		link TEXT NOT NULL,
		platform TEXT NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS idx_apps_platform ON apps(platform)`,
}

// Open opens (creating if needed) the SQLite database at path, applies the
// required pragmas, and creates the schema if it does not exist yet.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	// SQLite is a single-writer database; one connection avoids
	// "database is locked" contention between requests.
	db.SetMaxOpenConns(1)
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA foreign_keys=ON",
	} {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("%s: %w", pragma, err)
		}
	}
	s := &Store{DB: db}
	for _, stmt := range schemaStatements {
		if _, err := db.Exec(stmt); err != nil {
			db.Close()
			return nil, fmt.Errorf("schema: %w", err)
		}
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.DB.Close() }

// ErrNotFound is returned by lookups with no matching row.
var ErrNotFound = errors.New("not found")

func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

// ---- users ----

const userColumns = `id, username, password_hash, COALESCE(email,''), lastlogin, lastactivity, COALESCE(lastip,''), loginattempts, created, COALESCE(createdby,''), COALESCE(resethash,''), canreset`

func scanUser(row interface{ Scan(...any) error }) (*User, error) {
	u := &User{}
	err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Email, &u.LastLogin,
		&u.LastActivity, &u.LastIP, &u.LoginAttempts, &u.Created, &u.CreatedBy,
		&u.ResetHash, &u.CanReset)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return u, nil
}

// GetUserByUsername returns the user with the given username, or ErrNotFound.
func (s *Store) GetUserByUsername(username string) (*User, error) {
	return scanUser(s.DB.QueryRow(`SELECT `+userColumns+` FROM user WHERE username = ? LIMIT 1`, username))
}

// GetUserByID returns the user with the given id, or ErrNotFound.
func (s *Store) GetUserByID(id int64) (*User, error) {
	return scanUser(s.DB.QueryRow(`SELECT `+userColumns+` FROM user WHERE id = ? LIMIT 1`, id))
}

// CreateUser inserts a new user. It returns an error satisfying
// IsUniqueViolation when the username already exists.
func (s *Store) CreateUser(username, passwordHash, email string, created int64, createdBy, lastIP string) (int64, error) {
	res, err := s.DB.Exec(
		`INSERT INTO user (username, password_hash, email, created, createdby, lastip) VALUES (?, ?, ?, ?, ?, ?)`,
		username, passwordHash, email, created, createdBy, lastIP)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// IsUniqueViolation reports whether err is a UNIQUE constraint failure.
func IsUniqueViolation(err error) bool { return isUniqueViolation(err) }

// UpdatePasswordHash replaces a user's stored password hash (used for the
// transparent legacy->argon2id rehash and for `user passwd`).
func (s *Store) UpdatePasswordHash(userID int64, hash string) error {
	_, err := s.DB.Exec(`UPDATE user SET password_hash = ? WHERE id = ?`, hash, userID)
	return err
}

// ListUsers returns all users ordered by id.
func (s *Store) ListUsers() ([]*User, error) {
	rows, err := s.DB.Query(`SELECT ` + userColumns + ` FROM user ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// DeleteUserCascade deletes a user and all of their authcodes, links, and
// logincodes in one transaction.
func (s *Store) DeleteUserCascade(userID int64) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`DELETE FROM links WHERE userid = ?`,
		`DELETE FROM authcodes WHERE userid = ?`,
		`DELETE FROM logincodes WHERE userid = ?`,
		`DELETE FROM user WHERE id = ?`,
	} {
		if _, err := tx.Exec(q, userID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ---- authcodes ----

// InsertAuthCode creates a device auth code for a user.
func (s *Store) InsertAuthCode(userID int64, username, code, description string, created int64, createdBy string) error {
	_, err := s.DB.Exec(
		`INSERT INTO authcodes (userid, username, authhash, description, created, createdby) VALUES (?, ?, ?, ?, ?, ?)`,
		userID, username, code, description, created, createdBy)
	return err
}

// FindAuthCode looks up a device auth code by (username, authhash).
func (s *Store) FindAuthCode(username, code string) (*AuthCode, error) {
	row := s.DB.QueryRow(
		`SELECT id, userid, username, authhash, description, lastused, created, COALESCE(createdby,'')
		 FROM authcodes WHERE username = ? AND authhash = ? LIMIT 1`, username, code)
	ac := &AuthCode{}
	err := row.Scan(&ac.ID, &ac.UserID, &ac.Username, &ac.AuthHash, &ac.Description, &ac.LastUsed, &ac.Created, &ac.CreatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return ac, nil
}

func scanAuthCodes(rows *sql.Rows) ([]*AuthCode, error) {
	defer rows.Close()
	var out []*AuthCode
	for rows.Next() {
		ac := &AuthCode{}
		if err := rows.Scan(&ac.ID, &ac.UserID, &ac.Username, &ac.AuthHash, &ac.Description, &ac.LastUsed, &ac.Created, &ac.CreatedBy); err != nil {
			return nil, err
		}
		out = append(out, ac)
	}
	return out, rows.Err()
}

// ListAuthCodesByUserID returns a user's auth codes, newest first (the
// account API `devices` ordering).
func (s *Store) ListAuthCodesByUserID(userID int64) ([]*AuthCode, error) {
	rows, err := s.DB.Query(
		`SELECT id, userid, username, authhash, description, lastused, created, COALESCE(createdby,'')
		 FROM authcodes WHERE userid = ? ORDER BY created DESC, id DESC`, userID)
	if err != nil {
		return nil, err
	}
	return scanAuthCodes(rows)
}

// ListAuthCodesByUsername returns a user's auth codes, oldest first (CLI).
func (s *Store) ListAuthCodesByUsername(username string) ([]*AuthCode, error) {
	rows, err := s.DB.Query(
		`SELECT id, userid, username, authhash, description, lastused, created, COALESCE(createdby,'')
		 FROM authcodes WHERE username = ? ORDER BY id`, username)
	if err != nil {
		return nil, err
	}
	return scanAuthCodes(rows)
}

// DeleteAuthCode removes one auth code; returns rows affected.
func (s *Store) DeleteAuthCode(userID int64, username, code string) (int64, error) {
	res, err := s.DB.Exec(
		`DELETE FROM authcodes WHERE userid = ? AND username = ? AND authhash = ?`,
		userID, username, code)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ---- logincodes ----

// InsertLoginCode stores an account-API login token for a user.
func (s *Store) InsertLoginCode(userID int64, token string, now int64) error {
	_, err := s.DB.Exec(
		`INSERT INTO logincodes (userid, authhash, lastlogin, created) VALUES (?, ?, ?, ?)`,
		userID, token, now, now)
	return err
}

// FindLoginUser resolves an account-API login token to a user id, joining
// logincodes and user exactly like the original checkAuth().
func (s *Store) FindLoginUser(username, token string) (int64, error) {
	var userID int64
	err := s.DB.QueryRow(
		`SELECT user.id FROM logincodes JOIN user ON user.id = logincodes.userid
		 WHERE user.username = ? AND logincodes.authhash = ? LIMIT 1`,
		username, token).Scan(&userID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return userID, err
}

// ---- links ----

// UpsertLinks applies a batch of link updates in one transaction, using an
// INSERT ... ON CONFLICT upsert with the original insertLinks() semantics:
//
//   - comment counts only ever increase (MAX), comment time moves only when a
//     comment count is part of the update;
//   - lastvisit/firstvisit move only when the link itself is marked;
//   - firstvisit is set on first mark and never overwritten afterwards;
//   - lastcall always becomes the calling device, developers appends the dev.
//
// Updates for link ids shorter than 6 characters are skipped (the caller
// still counts them in its response, like the original).
func (s *Store) UpsertLinks(userID int64, dev, device string, updates []LinkUpdate, now int64) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`
		INSERT INTO links (linkid, userid, lastvisit, lastcommenttime, lastcommentcount, firstvisit, lastcall, developers)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(linkid, userid) DO UPDATE SET
			lastcommentcount = CASE WHEN ? THEN MAX(lastcommentcount, excluded.lastcommentcount) ELSE lastcommentcount END,
			lastcommenttime  = CASE WHEN ? THEN excluded.lastcommenttime ELSE lastcommenttime END,
			lastvisit        = CASE WHEN ? THEN excluded.lastvisit ELSE lastvisit END,
			firstvisit       = CASE WHEN ? THEN (CASE WHEN firstvisit = 0 THEN excluded.lastvisit ELSE firstvisit END) ELSE firstvisit END,
			lastcall         = excluded.lastcall,
			developers       = CASE WHEN developers IS NULL OR developers = '' THEN excluded.developers
			                        ELSE developers || ', ' || excluded.developers END`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, u := range updates {
		if len(u.LinkID) < 6 {
			continue
		}
		var commentCount, commentTime, lastVisit int64
		if u.HasComment {
			commentCount = u.CommentCount
			if commentCount < 0 {
				// The original column was MySQL unsigned: negative values
				// (the internal -1 sentinel) were clipped to 0. Store 0.
				commentCount = 0
			}
			commentTime = now
		}
		if u.MarkLink {
			lastVisit = now
		}
		hasComment := 0
		if u.HasComment {
			hasComment = 1
		}
		mark := 0
		if u.MarkLink {
			mark = 1
		}
		_, err := stmt.Exec(u.LinkID, userID, lastVisit, commentTime, commentCount, lastVisit, device, dev,
			hasComment, hasComment, mark, mark)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

const linkColumns = `id, linkid, userid, lastvisit, lastcommenttime, lastcommentcount, firstvisit, COALESCE(lastcall,''), COALESCE(developers,'')`

func scanLinks(rows *sql.Rows) ([]*Link, error) {
	defer rows.Close()
	var out []*Link
	for rows.Next() {
		l := &Link{}
		if err := rows.Scan(&l.ID, &l.LinkID, &l.UserID, &l.LastVisit, &l.LastCommentTime, &l.LastCommentCount, &l.FirstVisit, &l.LastCall, &l.Developers); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// ReadLinks returns the given user's rows for the requested link ids.
func (s *Store) ReadLinks(userID int64, ids []string) ([]*Link, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, 0, len(ids)+1)
	args = append(args, userID)
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := s.DB.Query(
		`SELECT `+linkColumns+` FROM links WHERE userid = ? AND linkid IN (`+placeholders+`) ORDER BY id`, args...)
	if err != nil {
		return nil, err
	}
	return scanLinks(rows)
}

// HistoryLinks returns a user's links changed at/after time (when time > 0),
// paged by offset with a page size of 100, like the original historyLinks().
func (s *Store) HistoryLinks(userID, since, offset int64) ([]*Link, error) {
	var rows *sql.Rows
	var err error
	if since > 0 {
		rows, err = s.DB.Query(
			`SELECT `+linkColumns+` FROM links
			 WHERE (lastvisit >= ? OR lastcommenttime >= ?) AND userid = ?
			 ORDER BY id LIMIT 100 OFFSET ?`, since, since, userID, offset)
	} else {
		rows, err = s.DB.Query(
			`SELECT `+linkColumns+` FROM links WHERE userid = ? ORDER BY id LIMIT 100 OFFSET ?`,
			userID, offset)
	}
	if err != nil {
		return nil, err
	}
	return scanLinks(rows)
}

// RecentLinks returns a user's 20 most recently visited links (account API
// history): ORDER BY lastvisit DESC LIMIT 20.
func (s *Store) RecentLinks(userID int64) ([]*Link, error) {
	rows, err := s.DB.Query(
		`SELECT `+linkColumns+` FROM links WHERE userid = ? ORDER BY lastvisit DESC LIMIT 20`, userID)
	if err != nil {
		return nil, err
	}
	return scanLinks(rows)
}

// ---- apps ----

// ListApps returns apps, optionally filtered by platform. An empty platform
// or "all" returns everything, like the original apps.php.
func (s *Store) ListApps(platform string) ([]*App, error) {
	var rows *sql.Rows
	var err error
	if platform == "" || platform == "all" {
		rows, err = s.DB.Query(`SELECT name, COALESCE(description,''), link, platform FROM apps ORDER BY id`)
	} else {
		rows, err = s.DB.Query(`SELECT name, COALESCE(description,''), link, platform FROM apps WHERE platform = ? ORDER BY id`, platform)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*App
	for rows.Next() {
		a := &App{}
		if err := rows.Scan(&a.Name, &a.Description, &a.Link, &a.Platform); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// InsertApp adds an app row (used by tests and manual seeding).
func (s *Store) InsertApp(name, description, link, platform string) error {
	_, err := s.DB.Exec(
		`INSERT INTO apps (name, description, link, platform) VALUES (?, ?, ?, ?)`,
		name, description, link, platform)
	return err
}
