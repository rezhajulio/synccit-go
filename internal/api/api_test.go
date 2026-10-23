// SPDX-License-Identifier: GPL-3.0-or-later
// Port of synccit by James Wilson / drakeapps (https://github.com/drakeapps/synccit), GPL-3.0-or-later.

package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezhajulio/synccit-go/internal/auth"
	"github.com/rezhajulio/synccit-go/internal/store"
)

func newTestHandler(t *testing.T, allowRegistration bool) (*store.Store, http.Handler) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st, NewServer(st, allowRegistration).Handler()
}

func createUser(t *testing.T, st *store.Store, username, password string) int64 {
	t.Helper()
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	id, err := st.CreateUser(username, hash, "", 1000, "test", "")
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func post(t *testing.T, h http.Handler, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func postJSON(t *testing.T, h http.Handler, path, payload string) *httptest.ResponseRecorder {
	t.Helper()
	return post(t, h, path, url.Values{"data": {payload}, "type": {"json"}})
}

type errResp struct {
	Error   string `json:"error"`
	Success string `json:"success"`
	Device  string `json:"device"`
	Auth    string `json:"auth"`
	Login   string `json:"login"`
}

func decodeResp(t *testing.T, rec *httptest.ResponseRecorder) errResp {
	t.Helper()
	var r errResp
	if err := json.Unmarshal(rec.Body.Bytes(), &r); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return r
}

type readItem struct {
	ID           string `json:"id"`
	LastVisit    string `json:"lastvisit"`
	Comments     string `json:"comments"`
	CommentVisit string `json:"commentvisit"`
	Type         string `json:"type"`
}

func decodeRead(t *testing.T, rec *httptest.ResponseRecorder) []readItem {
	t.Helper()
	var items []readItem
	if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
		t.Fatalf("decode read %q: %v", rec.Body.String(), err)
	}
	return items
}

// The README scenario: 111111 plain, 222222 comments-only (132), 333333 both
// (313 comments + link), 444444 plain. Reading them plus unknown 555555
// returns the four with the right per-field state.
func TestSyncUpdateReadJSON(t *testing.T) {
	st, h := newTestHandler(t, false)
	uid := createUser(t, st, "james", "password123")
	if err := st.InsertAuthCode(uid, "james", "9m89x0", "test device", 1000, "test"); err != nil {
		t.Fatal(err)
	}

	update := `{"username":"james","auth":"9m89x0","dev":"synccit json","mode":"update","links":[
		{"id":"111111"},
		{"id":"222222","comments":"132"},
		{"id":"333333","comments":313,"both":true},
		{"id":"444444"}
	]}`
	rec := postJSON(t, h, "/api.php", update)
	if got := decodeResp(t, rec); got.Success != "4 links updated" {
		t.Fatalf("update success = %q, body %s", got.Success, rec.Body.String())
	}
	if rec.Header().Get("X-API") != "1" || rec.Header().Get("X-Revision") != "12" {
		t.Fatalf("version headers = %q / %q", rec.Header().Get("X-API"), rec.Header().Get("X-Revision"))
	}
	if rec.Header().Get("X-Success") != "4 links updated" {
		t.Fatalf("X-Success = %q", rec.Header().Get("X-Success"))
	}

	read := `{"username":"james","auth":"9m89x0","dev":"synccit json","mode":"read","links":[
		{"id":"111111"},{"id":"222222"},{"id":"333333"},{"id":"444444"},{"id":"555555"}
	]}`
	items := decodeRead(t, postJSON(t, h, "/api.php", read))
	if len(items) != 4 {
		t.Fatalf("read returned %d items, want 4: %+v", len(items), items)
	}
	byID := map[string]readItem{}
	for _, it := range items {
		byID[it.ID] = it
	}
	if it := byID["111111"]; it.LastVisit == "0" || it.LastVisit == "" || it.Comments != "0" || it.CommentVisit != "0" {
		t.Errorf("111111 (plain) wrong: %+v", it)
	}
	if it := byID["222222"]; it.LastVisit != "0" || it.Comments != "132" || it.CommentVisit == "0" {
		t.Errorf("222222 (comments-only) wrong: %+v", it)
	}
	if it := byID["333333"]; it.LastVisit == "0" || it.Comments != "313" || it.CommentVisit == "0" {
		t.Errorf("333333 (both) wrong: %+v", it)
	}
	if it := byID["444444"]; it.LastVisit == "0" || it.Comments != "0" || it.CommentVisit != "0" {
		t.Errorf("444444 (plain) wrong: %+v", it)
	}

	// Read of a never-touched link returns [].
	rec = postJSON(t, h, "/api", `{"username":"james","auth":"9m89x0","mode":"read","links":[{"id":"999999"}]}`)
	if rec.Body.String() != "[]" {
		t.Fatalf("unknown read = %q, want []", rec.Body.String())
	}
	// Read with no links is an error.
	rec = postJSON(t, h, "/api.php", `{"username":"james","auth":"9m89x0","mode":"read","links":[]}`)
	if got := decodeResp(t, rec); got.Error != "no links requested" {
		t.Fatalf("empty read error = %q", got.Error)
	}
	// Bad auth.
	rec = postJSON(t, h, "/api.php", `{"username":"james","auth":"zzzzzz","mode":"read","links":[{"id":"111111"}]}`)
	if got := decodeResp(t, rec); got.Error != "not authorized" {
		t.Fatalf("bad auth error = %q", got.Error)
	}
	if rec.Header().Get("X-Error") != "not authorized" {
		t.Fatalf("X-Error = %q", rec.Header().Get("X-Error"))
	}

	// History: all four, paging by offset, filtered by time.
	hist := decodeRead(t, postJSON(t, h, "/api.php",
		`{"username":"james","auth":"9m89x0","mode":"history","time":0,"offset":0}`))
	if len(hist) != 4 {
		t.Fatalf("history = %d items, want 4", len(hist))
	}
	page := decodeRead(t, postJSON(t, h, "/api.php",
		`{"username":"james","auth":"9m89x0","mode":"history","time":0,"offset":3}`))
	if len(page) != 1 {
		t.Fatalf("history offset 3 = %d items, want 1", len(page))
	}
	rec = postJSON(t, h, "/api.php",
		`{"username":"james","auth":"9m89x0","mode":"history","time":9999999999,"offset":0}`)
	if rec.Body.String() != "[]" {
		t.Fatalf("history future = %q, want []", rec.Body.String())
	}
}

func TestCreateGatingAndValidation(t *testing.T) {
	// Registration locked by default.
	_, h := newTestHandler(t, false)
	rec := postJSON(t, h, "/api.php",
		`{"username":"newuser","password":"thebestpasswordever","dev":"demo","email":"n@x.com","mode":"create"}`)
	if got := decodeResp(t, rec); got.Error != "registration disabled" {
		t.Fatalf("locked create = %q, want 'registration disabled'", got.Error)
	}

	// Registration open.
	st, h2 := newTestHandler(t, true)
	rec = postJSON(t, h2, "/api.php",
		`{"username":"newuser","password":"thebestpasswordever","dev":"demo","email":"n@x.com","mode":"create"}`)
	if got := decodeResp(t, rec); got.Success != "account created" {
		t.Fatalf("create = %+v", got)
	}
	rec = postJSON(t, h2, "/api.php",
		`{"username":"newuser","password":"thebestpasswordever","mode":"create"}`)
	if got := decodeResp(t, rec); got.Error != "username already exists" {
		t.Fatalf("duplicate = %q", got.Error)
	}
	cases := []struct{ payload, want string }{
		{`{"username":"ab","password":"longenough","mode":"create"}`, "username needs to be at least 3 characters long"},
		{`{"username":"validname","password":"short","mode":"create"}`, "password needs to be at least 6 characters long"},
		{`{"username":"validname","password":"longenough","email":"nope","mode":"create"}`, "email not valid"},
		{`{"username":"bad name","password":"longenough","mode":"create"}`, "username must consist of letters, numbers, or underscores"},
	}
	for _, c := range cases {
		rec = postJSON(t, h2, "/api.php", c.payload)
		if got := decodeResp(t, rec); got.Error != c.want {
			t.Errorf("payload %s: error = %q, want %q", c.payload, got.Error, c.want)
		}
	}

	// The created account can immediately sync using its password as auth
	// (the checkAuth password fallback).
	u, err := st.GetUserByUsername("newuser")
	if err != nil {
		t.Fatal(err)
	}
	if u.CreatedBy != "demo" {
		t.Errorf("createdby = %q, want demo", u.CreatedBy)
	}
	rec = postJSON(t, h2, "/api.php",
		`{"username":"newuser","auth":"thebestpasswordever","mode":"update","links":[{"id":"abc123"}]}`)
	if got := decodeResp(t, rec); got.Success != "1 links updated" {
		t.Fatalf("password-fallback update = %+v", got)
	}
}

func TestAddAuthFlow(t *testing.T) {
	st, h := newTestHandler(t, false)
	createUser(t, st, "james", "password123")

	rec := postJSON(t, h, "/api.php",
		`{"username":"nosuch","password":"password123","device":"phone","mode":"addauth"}`)
	if got := decodeResp(t, rec); got.Error != "user not found" {
		t.Fatalf("unknown user addauth = %q", got.Error)
	}
	rec = postJSON(t, h, "/api.php",
		`{"username":"james","password":"wrong-password","device":"phone","mode":"addauth"}`)
	if got := decodeResp(t, rec); got.Error != "username or password incorrect" {
		t.Fatalf("wrong pw addauth = %q", got.Error)
	}
	rec = postJSON(t, h, "/api.php",
		`{"username":"james","password":"password123","device":"my phone","dev":"test","mode":"addauth"}`)
	got := decodeResp(t, rec)
	if got.Success != "device key added" || got.Device != "my phone" || len(got.Auth) != 6 {
		t.Fatalf("addauth = %+v", got)
	}
	if rec.Header().Get("X-Success") != "device key added" {
		t.Fatalf("X-Success = %q", rec.Header().Get("X-Success"))
	}
	// The minted code works for update + read.
	rec = postJSON(t, h, "/api.php",
		`{"username":"james","auth":"`+got.Auth+`","mode":"update","links":[{"id":"xyz789"}]}`)
	if r := decodeResp(t, rec); r.Success != "1 links updated" {
		t.Fatalf("update with new code = %+v", r)
	}
	items := decodeRead(t, postJSON(t, h, "/api.php",
		`{"username":"james","auth":"`+got.Auth+`","mode":"read","links":[{"id":"xyz789"}]}`))
	if len(items) != 1 || items[0].ID != "xyz789" {
		t.Fatalf("read with new code = %+v", items)
	}
}

func TestPlaintextRoundTrip(t *testing.T) {
	st, h := newTestHandler(t, false)
	uid := createUser(t, st, "james", "password123")
	_ = st.InsertAuthCode(uid, "james", "9m89x0", "browser", 1000, "test")

	rec := post(t, h, "/api.php", url.Values{
		"username": {"james"}, "auth": {"9m89x0"}, "mode": {"update"},
		"links": {"111111,222222"}, "comments": {"222222:42"}, "dev": {"userscript"},
	})
	if rec.Body.String() != "success: 2 links updated" {
		t.Fatalf("plaintext update = %q", rec.Body.String())
	}
	rec = post(t, h, "/api.php", url.Values{
		"username": {"james"}, "auth": {"9m89x0"}, "mode": {"read"},
		"links": {"111111,222222"},
	})
	body := rec.Body.String()
	if !strings.Contains(body, "111111:") || !strings.Contains(body, ";0:0,\n") {
		t.Fatalf("plaintext read missing 111111 row: %q", body)
	}
	if !strings.Contains(body, "222222:") || !strings.Contains(body, ";42:") {
		t.Fatalf("plaintext read missing 222222 comment count: %q", body)
	}
	rec = post(t, h, "/api.php", url.Values{
		"username": {"james"}, "auth": {"badbad"}, "mode": {"read"}, "links": {"111111"},
	})
	if rec.Body.String() != "error: not authorized" {
		t.Fatalf("plaintext bad auth = %q", rec.Body.String())
	}
	rec = post(t, h, "/api.php", url.Values{"something": {"else"}})
	if rec.Body.String() != "error: no post data" {
		t.Fatalf("plaintext no data = %q", rec.Body.String())
	}
}

func TestXMLRoundTrip(t *testing.T) {
	st, h := newTestHandler(t, false)
	uid := createUser(t, st, "james", "password123")
	_ = st.InsertAuthCode(uid, "james", "9m89x0", "browser", 1000, "test")

	updateXML := `<?xml version="1.0"?>
<synccit><username>james</username><auth>9m89x0</auth><dev>synccit xml</dev><mode>update</mode>
<links><link><id>777777</id><comments>55</comments><both>true</both></link><link><id>888888</id></link></links></synccit>`
	rec := post(t, h, "/api.php", url.Values{"data": {updateXML}, "type": {"xml"}})
	if ct := rec.Header().Get("Content-Type"); ct != "application/rss+xml" {
		t.Fatalf("xml content type = %q", ct)
	}
	if !strings.Contains(rec.Body.String(), "<success>2 links updated</success>") {
		t.Fatalf("xml update = %q", rec.Body.String())
	}

	readXML := `<?xml version="1.0"?>
<synccit><username>james</username><auth>9m89x0</auth><mode>read</mode>
<links><link><id>777777</id></link><link><id>888888</id></link></links></synccit>`
	rec = post(t, h, "/api.php", url.Values{"data": {readXML}, "type": {"xml"}})
	body := rec.Body.String()
	if !strings.Contains(body, "<id>777777</id>") || !strings.Contains(body, "<comments>55</comments>") {
		t.Fatalf("xml read = %q", body)
	}
	// XML detection by payload prefix, no type field.
	rec = post(t, h, "/api.php", url.Values{"data": {readXML}})
	if !strings.Contains(rec.Body.String(), "<id>777777</id>") {
		t.Fatalf("xml sniff read = %q", rec.Body.String())
	}
	// Malformed inputs.
	rec = post(t, h, "/api.php", url.Values{"data": {"{not json"}, "type": {"json"}})
	if !strings.Contains(rec.Body.String(), "json error") {
		t.Fatalf("malformed json = %q", rec.Body.String())
	}
	rec = post(t, h, "/api.php", url.Values{"data": {"<?xml version=\"1.0\"?><synccit><unclosed>"}, "type": {"xml"}})
	if !strings.Contains(rec.Body.String(), "<error>xml error</error>") {
		t.Fatalf("malformed xml = %q", rec.Body.String())
	}
}

func TestAccountAPI(t *testing.T) {
	st, h := newTestHandler(t, false)
	createUser(t, st, "james", "password123")

	// No data at all -> plain "error: no post data".
	rec := post(t, h, "/account.php", url.Values{})
	if rec.Body.String() != "error: no post data" {
		t.Fatalf("account no data = %q", rec.Body.String())
	}
	// Login failures.
	rec = postJSON(t, h, "/account.php", `{"username":"ghost","password":"password123","mode":"login"}`)
	if got := decodeResp(t, rec); got.Error != "user not found" {
		t.Fatalf("login ghost = %q", got.Error)
	}
	rec = postJSON(t, h, "/account.php", `{"username":"james","password":"nope-nope","mode":"login"}`)
	if got := decodeResp(t, rec); got.Error != "username or password incorrect" {
		t.Fatalf("login wrong pw = %q", got.Error)
	}
	// Login success.
	rec = postJSON(t, h, "/account.php", `{"username":"james","password":"password123","dev":"acct test","mode":"login"}`)
	got := decodeResp(t, rec)
	if got.Success != "login hash added" || len(got.Login) != 64 {
		t.Fatalf("login = %+v", got)
	}
	if rec.Header().Get("X-Revision") != "1" {
		t.Fatalf("account X-Revision = %q", rec.Header().Get("X-Revision"))
	}
	token := got.Login

	// Devices starts empty.
	rec = postJSON(t, h, "/account.php", `{"username":"james","login":"`+token+`","mode":"devices"}`)
	if rec.Body.String() != "[]" {
		t.Fatalf("devices empty = %q", rec.Body.String())
	}
	// Bad token.
	rec = postJSON(t, h, "/account.php", `{"username":"james","login":"deadbeef","mode":"devices"}`)
	if got := decodeResp(t, rec); got.Error != "not authorized" {
		t.Fatalf("bad token = %q", got.Error)
	}
	// addauth via token (no password).
	rec = postJSON(t, h, "/account",
		`{"username":"james","login":"`+token+`","device":"phone","dev":"acct test","mode":"addauth"}`)
	got = decodeResp(t, rec)
	if got.Success != "device key added" || len(got.Auth) != 6 {
		t.Fatalf("account addauth = %+v", got)
	}
	code := got.Auth

	// Devices lists the new code with createdby.
	rec = postJSON(t, h, "/account.php", `{"username":"james","login":"`+token+`","mode":"devices"}`)
	var devices []struct {
		Auth      string `json:"auth"`
		Device    string `json:"device"`
		Created   string `json:"created"`
		CreatedBy string `json:"createdby"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &devices); err != nil {
		t.Fatal(err)
	}
	if len(devices) != 1 || devices[0].Auth != code || devices[0].Device != "phone" || devices[0].CreatedBy != "acct test" {
		t.Fatalf("devices = %+v", devices)
	}

	// Use the device code on the sync API, then account history shows it
	// with "type":"link".
	rec = postJSON(t, h, "/api.php",
		`{"username":"james","auth":"`+code+`","mode":"update","links":[{"id":"hist01"}]}`)
	if r := decodeResp(t, rec); r.Success != "1 links updated" {
		t.Fatalf("sync update = %+v", r)
	}
	items := decodeRead(t, postJSON(t, h, "/account.php",
		`{"username":"james","login":"`+token+`","mode":"history"}`))
	if len(items) != 1 || items[0].ID != "hist01" || items[0].Type != "link" {
		t.Fatalf("account history = %+v", items)
	}

	// Delete the device; the original answers "N links updated".
	rec = postJSON(t, h, "/account.php",
		`{"username":"james","login":"`+token+`","mode":"delete","devices":[{"id":"`+code+`"}]}`)
	if got := decodeResp(t, rec); got.Success != "1 links updated" {
		t.Fatalf("account delete = %+v", got)
	}
	rec = postJSON(t, h, "/account.php", `{"username":"james","login":"`+token+`","mode":"devices"}`)
	if rec.Body.String() != "[]" {
		t.Fatalf("devices after delete = %q", rec.Body.String())
	}
}

func TestAppsAPI(t *testing.T) {
	st, h := newTestHandler(t, false)
	rec := post(t, h, "/api/apps.php", url.Values{})
	if rec.Body.String() != "[]" {
		t.Fatalf("apps empty = %q", rec.Body.String())
	}
	_ = st.InsertApp("Android App", "for android", "https://example.com/android", "android")
	_ = st.InsertApp("iOS App", "for ios", "https://example.com/ios", "ios")

	rec = post(t, h, "/apps", url.Values{})
	var all []appJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &all); err != nil || len(all) != 2 {
		t.Fatalf("apps all = %q err %v", rec.Body.String(), err)
	}
	rec = post(t, h, "/api/apps.php", url.Values{"data": {`{"platform":"android"}`}})
	var filtered []appJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &filtered); err != nil || len(filtered) != 1 || filtered[0].Name != "Android App" {
		t.Fatalf("apps android = %q err %v", rec.Body.String(), err)
	}
	rec = post(t, h, "/api/apps.php", url.Values{"data": {`{"platform":"all"}`}})
	if err := json.Unmarshal(rec.Body.Bytes(), &all); err != nil || len(all) != 2 {
		t.Fatalf("apps platform=all = %q", rec.Body.String())
	}
}

// Legacy users (PBKDF2-SHA512 rows in the imported format) can still log in
// everywhere a password is checked, and get transparently rehashed to
// argon2id. Vector cross-checked against the original PHP algorithm (see
// internal/auth tests).
const (
	legacyPassword = "correct horse battery staple"
	legacyStored   = "pbkdf2-sha512$10000$AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8gISIjJCUmJygpKissLS4vMDEyMzQ1Njc4OTo7PD0+Pw==$3HNFH3QN9wFnS/SoYG7erPNKNHDDe3MAH6wNrnE/EuTdtP1vj5h8NzHesw0gq+SkfcutP1g6HapkbDhZC901dA=="
)

func createLegacyUser(t *testing.T, st *store.Store, username string) {
	t.Helper()
	if _, err := st.CreateUser(username, legacyStored, "", 1000, "import", ""); err != nil {
		t.Fatal(err)
	}
}

func TestLegacyPasswordUpgrade(t *testing.T) {
	st, h := newTestHandler(t, false)

	// Path 1: sync addauth with the legacy password.
	createLegacyUser(t, st, "legacy1")
	rec := postJSON(t, h, "/api.php",
		`{"username":"legacy1","password":"`+legacyPassword+`","device":"phone","mode":"addauth"}`)
	if got := decodeResp(t, rec); got.Success != "device key added" {
		t.Fatalf("legacy addauth = %+v (body %s)", got, rec.Body.String())
	}
	u, err := st.GetUserByUsername("legacy1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(u.PasswordHash, "$argon2id$") {
		t.Fatalf("hash not upgraded after addauth: %q", u.PasswordHash[:20])
	}

	// Path 2: password as auth (checkAuth fallback) on update.
	createLegacyUser(t, st, "legacy2")
	rec = postJSON(t, h, "/api.php",
		`{"username":"legacy2","auth":"`+legacyPassword+`","mode":"update","links":[{"id":"leg001"}]}`)
	if got := decodeResp(t, rec); got.Success != "1 links updated" {
		t.Fatalf("legacy fallback update = %+v", got)
	}
	u, _ = st.GetUserByUsername("legacy2")
	if !strings.HasPrefix(u.PasswordHash, "$argon2id$") {
		t.Fatalf("hash not upgraded after fallback auth")
	}

	// Path 3: account login.
	createLegacyUser(t, st, "legacy3")
	rec = postJSON(t, h, "/account.php",
		`{"username":"legacy3","password":"`+legacyPassword+`","mode":"login"}`)
	if got := decodeResp(t, rec); got.Success != "login hash added" {
		t.Fatalf("legacy account login = %+v", got)
	}
	u, _ = st.GetUserByUsername("legacy3")
	if !strings.HasPrefix(u.PasswordHash, "$argon2id$") {
		t.Fatalf("hash not upgraded after account login")
	}

	// Wrong legacy password is still rejected.
	createLegacyUser(t, st, "legacy4")
	rec = postJSON(t, h, "/account.php",
		`{"username":"legacy4","password":"wrong password","mode":"login"}`)
	if got := decodeResp(t, rec); got.Error != "username or password incorrect" {
		t.Fatalf("legacy wrong pw = %+v", got)
	}
}
