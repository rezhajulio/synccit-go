// SPDX-License-Identifier: GPL-3.0-or-later
// Port of synccit by James Wilson / drakeapps (https://github.com/drakeapps/synccit), GPL-3.0-or-later.

// Package api implements the synccit HTTP APIs: the sync API (/api.php), the
// account API (/account.php), and the apps API (/api/apps.php), wire-compatible
// with the original PHP implementation.
package api

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/rezhajulio/synccit-go/internal/auth"
	"github.com/rezhajulio/synccit-go/internal/store"
)

// Output formats, mirroring the original type=json / type=xml / plain text.
type format int

const (
	formatJSON format = iota
	formatXML
	formatText
)

// Server is the synccit API server.
type Server struct {
	store             *store.Store
	allowRegistration bool
}

// NewServer builds a Server over the given store.
func NewServer(st *store.Store, allowRegistration bool) *Server {
	return &Server{store: st, allowRegistration: allowRegistration}
}

// Handler returns the HTTP routing table: the original .php paths plus clean
// aliases handled identically.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api.php", s.handleSync)
	mux.HandleFunc("POST /api", s.handleSync)
	mux.HandleFunc("POST /account.php", s.handleAccount)
	mux.HandleFunc("POST /account", s.handleAccount)
	mux.HandleFunc("POST /api/apps.php", s.handleApps)
	mux.HandleFunc("POST /apps", s.handleApps)
	return mux
}

// ---- shared request representation ----

type linkInput struct {
	ID              string
	CommentsPresent bool
	Comments        int64
	Both            bool
}

type requestData struct {
	Username string
	Auth     string
	Login    string
	Dev      string
	Mode     string
	Password string
	Email    string
	Device   string
	Time     int64
	Offset   int64
	Links    []linkInput
	Devices  []string // device auth codes referenced by id (account delete)
}

// flexInt accepts a JSON number or a numeric JSON string.
type flexInt int64

func (f *flexInt) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		*f = 0
		return nil
	}
	var n int64
	if err := json.Unmarshal(b, &n); err == nil {
		*f = flexInt(n)
		return nil
	}
	var str string
	if err := json.Unmarshal(b, &str); err == nil {
		v, _ := strconv.ParseInt(strings.TrimSpace(str), 10, 64)
		*f = flexInt(v)
		return nil
	}
	var fl float64
	if err := json.Unmarshal(b, &fl); err == nil {
		*f = flexInt(fl)
		return nil
	}
	*f = 0
	return nil
}

// flexLink is one entry of a JSON `links` array.
type flexLink struct {
	input linkInput
}

func parseFlexBool(b []byte) bool {
	switch strings.TrimSpace(string(b)) {
	case "true", `"true"`, `"1"`, "1":
		return true
	}
	return false
}

func parseFlexIntValue(b []byte) int64 {
	var f flexInt
	_ = f.UnmarshalJSON(b)
	return int64(f)
}

func (l *flexLink) UnmarshalJSON(b []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	if v, ok := raw["id"]; ok {
		var s string
		if err := json.Unmarshal(v, &s); err == nil {
			l.input.ID = s
		} else {
			l.input.ID = strings.Trim(string(v), `"`)
		}
	}
	if v, ok := raw["comments"]; ok && string(v) != "null" {
		l.input.CommentsPresent = true
		l.input.Comments = parseFlexIntValue(v)
	}
	if v, ok := raw["both"]; ok {
		l.input.Both = parseFlexBool(v)
	}
	return nil
}

type deviceRef struct {
	ID string `json:"id"`
}

type jsonRequest struct {
	Username string      `json:"username"`
	Auth     string      `json:"auth"`
	Login    string      `json:"login"`
	Dev      *string     `json:"dev"`
	Mode     string      `json:"mode"`
	Password string      `json:"password"`
	Email    string      `json:"email"`
	Device   string      `json:"device"`
	Time     flexInt     `json:"time"`
	Offset   flexInt     `json:"offset"`
	Links    []flexLink  `json:"links"`
	Devices  []deviceRef `json:"devices"`
}

func (j *jsonRequest) toRequestData() *requestData {
	rd := &requestData{
		Username: j.Username,
		Auth:     j.Auth,
		Login:    j.Login,
		Dev:      "unknown",
		Mode:     j.Mode,
		Password: j.Password,
		Email:    j.Email,
		Device:   j.Device,
		Time:     int64(j.Time),
		Offset:   int64(j.Offset),
	}
	if j.Dev != nil {
		rd.Dev = *j.Dev
	}
	for _, l := range j.Links {
		rd.Links = append(rd.Links, l.input)
	}
	for _, d := range j.Devices {
		rd.Devices = append(rd.Devices, d.ID)
	}
	return rd
}

type xmlLink struct {
	ID       string  `xml:"id"`
	Comments *string `xml:"comments"`
	Both     *string `xml:"both"`
}

type xmlDevice struct {
	ID string `xml:"id"`
}

type xmlRequest struct {
	XMLName  xml.Name    `xml:"synccit"`
	Username string      `xml:"username"`
	Auth     string      `xml:"auth"`
	Login    string      `xml:"login"`
	Dev      *string     `xml:"dev"`
	Mode     string      `xml:"mode"`
	Password string      `xml:"password"`
	Email    string      `xml:"email"`
	Device   string      `xml:"device"`
	Time     string      `xml:"time"`
	Offset   string      `xml:"offset"`
	Links    []xmlLink   `xml:"links>link"`
	Devices  []xmlDevice `xml:"devices>device"`
}

func (x *xmlRequest) toRequestData() *requestData {
	rd := &requestData{
		Username: x.Username,
		Auth:     x.Auth,
		Login:    x.Login,
		Dev:      "unknown",
		Mode:     x.Mode,
		Password: x.Password,
		Email:    x.Email,
		Device:   x.Device,
	}
	if x.Dev != nil {
		rd.Dev = *x.Dev
	}
	rd.Time, _ = strconv.ParseInt(strings.TrimSpace(x.Time), 10, 64)
	rd.Offset, _ = strconv.ParseInt(strings.TrimSpace(x.Offset), 10, 64)
	for _, l := range x.Links {
		li := linkInput{ID: l.ID}
		if l.Comments != nil {
			li.CommentsPresent = true
			li.Comments, _ = strconv.ParseInt(strings.TrimSpace(*l.Comments), 10, 64)
		}
		if l.Both != nil {
			v := strings.TrimSpace(*l.Both)
			li.Both = v == "true" || v == "1"
		}
		rd.Links = append(rd.Links, li)
	}
	for _, d := range x.Devices {
		rd.Devices = append(rd.Devices, d.ID)
	}
	return rd
}

// parseDataRequest parses a request carrying a POST form field `data`
// (JSON or XML). It reports false when an error response has already been
// written (or when there is no data field at all — the caller decides what
// that means).
func parseDataRequest(w http.ResponseWriter, r *http.Request) (rd *requestData, f format, hasData bool, ok bool) {
	_ = r.ParseForm()
	_, hasData = r.PostForm["data"]
	if !hasData {
		return nil, formatText, false, true
	}
	data := r.PostForm.Get("data")
	if strings.HasPrefix(data, "<?xml") || strings.EqualFold(r.Form.Get("type"), "xml") {
		f = formatXML
		var xr xmlRequest
		if err := xml.Unmarshal([]byte(data), &xr); err != nil {
			writeError(w, formatXML, "xml error")
			return nil, f, true, false
		}
		return xr.toRequestData(), f, true, true
	}
	f = formatJSON
	var jr jsonRequest
	if err := json.Unmarshal([]byte(data), &jr); err != nil {
		// The original prints "json error ".json_last_error(); 4 is PHP's
		// JSON_ERROR_SYNTAX.
		writeError(w, formatJSON, "json error 4")
		return nil, f, true, false
	}
	return jr.toRequestData(), f, true, true
}

// ---- response writers (match the original xerror/xsuccess/xdevice/xlogin) ----

func marshalJSON(v any) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
	return strings.TrimSuffix(buf.String(), "\n")
}

func xmlEscape(s string) string {
	var buf bytes.Buffer
	_ = xml.EscapeText(&buf, []byte(s))
	return buf.String()
}

func xmlDoc(inner string) string {
	return `<?xml version="1.0"?>` + "\n" + inner
}

func writeError(w http.ResponseWriter, f format, msg string) {
	w.Header().Set("X-Error", msg)
	switch f {
	case formatJSON:
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(marshalJSON(map[string]string{"error": msg})))
	case formatXML:
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write([]byte(xmlDoc(`<synccit><error>` + xmlEscape(msg) + `</error></synccit>`)))
	default:
		_, _ = w.Write([]byte("error: " + msg))
	}
}

func writeSuccess(w http.ResponseWriter, f format, msg string) {
	w.Header().Set("X-Success", msg)
	switch f {
	case formatJSON:
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(marshalJSON(map[string]string{"success": msg})))
	case formatXML:
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write([]byte(xmlDoc(`<synccit><success>` + xmlEscape(msg) + `</success></synccit>`)))
	default:
		_, _ = w.Write([]byte("success: " + msg))
	}
}

func writeDevice(w http.ResponseWriter, f format, device, code string) {
	w.Header().Set("X-Success", "device key added")
	switch f {
	case formatJSON:
		w.Header().Set("Content-Type", "application/json")
		body := marshalJSON(struct {
			Success string `json:"success"`
			Device  string `json:"device"`
			Auth    string `json:"auth"`
		}{"device key added", device, code})
		_, _ = w.Write([]byte(body))
	case formatXML:
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write([]byte(xmlDoc(`<synccit><success>device key added</success><device>` +
			xmlEscape(device) + `</device><auth>` + xmlEscape(code) + `</auth></synccit>`)))
	default:
		_, _ = w.Write([]byte("auth: " + code))
	}
}

func writeLogin(w http.ResponseWriter, f format, token string) {
	w.Header().Set("X-Success", "login hash added")
	switch f {
	case formatJSON:
		w.Header().Set("Content-Type", "application/json")
		body := marshalJSON(struct {
			Success string `json:"success"`
			Login   string `json:"login"`
		}{"login hash added", token})
		_, _ = w.Write([]byte(body))
	case formatXML:
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write([]byte(xmlDoc(`<synccit><success>login hash added</success><login>` +
			xmlEscape(token) + `</login></synccit>`)))
	}
}

// ---- shared helpers ----

func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// verifyPassword checks a password against a user row and, on a successful
// match against a legacy PBKDF2 hash, transparently rehashes to argon2id.
func (s *Server) verifyPassword(u *store.User, password string) bool {
	ok, legacy := auth.VerifyPassword(password, u.PasswordHash)
	if ok && legacy {
		if h, err := auth.HashPassword(password); err == nil {
			_ = s.store.UpdatePasswordHash(u.ID, h)
		}
	}
	return ok
}

// createAccount implements the shared create mode of both APIs, including
// the registration gate. It always writes a response.
func (s *Server) createAccount(w http.ResponseWriter, r *http.Request, req *requestData, f format) {
	if !s.allowRegistration {
		writeError(w, f, "registration disabled")
		return
	}
	if msg := auth.ValidateAccount(req.Username, req.Password, req.Email); msg != "" {
		writeError(w, f, msg)
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeError(w, f, "database error")
		return
	}
	_, err = s.store.CreateUser(req.Username, hash, req.Email, nowUnix(), req.Dev, remoteIP(r))
	if err != nil {
		if store.IsUniqueViolation(err) {
			writeError(w, f, "username already exists")
		} else {
			writeError(w, f, "database error")
		}
		return
	}
	writeSuccess(w, f, "account created")
}

// addAuthCode generates and stores a device auth code for a user.
func (s *Server) addAuthCode(userID int64, username, device, dev string) (string, error) {
	code, err := auth.GenerateAuthCode()
	if err != nil {
		return "", err
	}
	if err := s.store.InsertAuthCode(userID, username, code, device, nowUnix(), dev); err != nil {
		return "", err
	}
	return code, nil
}
