// SPDX-License-Identifier: GPL-3.0-or-later
// Port of synccit by James Wilson / drakeapps (https://github.com/drakeapps/synccit), GPL-3.0-or-later.

package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/rezhajulio/synccit-go/internal/auth"
	"github.com/rezhajulio/synccit-go/internal/store"
)

// handleAccount implements the account API (account.php, X-Revision: 1):
// JSON and XML only, authenticated by login token instead of device code.
func (s *Server) handleAccount(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-API", "1")
	w.Header().Set("X-Revision", "1")

	req, f, hasData, ok := parseDataRequest(w, r)
	if !ok {
		return
	}
	if !hasData {
		// The original calls xerror("no post data") with no mode: plain text.
		writeError(w, formatText, "no post data")
		return
	}

	switch req.Mode {
	case "create":
		s.createAccount(w, r, req, f)
		return
	case "login":
		s.accountLogin(w, req, f)
		return
	}

	token := req.Login
	if token == "" {
		token = req.Auth
	}
	userID, err := s.store.FindLoginUser(req.Username, token)
	if err != nil {
		writeError(w, f, "not authorized")
		return
	}

	switch req.Mode {
	case "delete":
		for _, code := range req.Devices {
			// The original ignores per-row success/failure here.
			_, _ = s.store.DeleteAuthCode(userID, req.Username, code)
		}
		// Yes, the original really does answer "N links updated" for a
		// device delete — copy-paste quirk preserved for compatibility.
		writeSuccess(w, f, fmt.Sprintf("%d links updated", len(req.Devices)))
	case "history":
		links, err := s.store.RecentLinks(userID)
		if err != nil {
			writeError(w, f, "database error")
			return
		}
		writeAccountHistory(w, f, links)
	case "devices":
		codes, err := s.store.ListAuthCodesByUserID(userID)
		if err != nil {
			writeError(w, f, "database error")
			return
		}
		writeDevices(w, f, codes)
	case "addauth":
		// Account-API addauth is authorized by the login token; no password.
		code, err := s.addAuthCode(userID, req.Username, req.Device, req.Dev)
		if err != nil {
			writeError(w, f, "database error")
			return
		}
		writeDevice(w, f, req.Device, code)
	default:
		// The original falls through with an empty response.
	}
}

// accountLogin implements mode=login: verify username+password and mint a
// login token stored in logincodes.
func (s *Server) accountLogin(w http.ResponseWriter, req *requestData, f format) {
	u, err := s.store.GetUserByUsername(req.Username)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, f, "user not found")
		return
	}
	if err != nil {
		writeError(w, f, "database error")
		return
	}
	if !s.verifyPassword(u, req.Password) {
		writeError(w, f, "username or password incorrect")
		return
	}
	token, err := auth.GenerateLoginToken()
	if err != nil {
		writeError(w, f, "database error")
		return
	}
	if err := s.store.InsertLoginCode(u.ID, token, nowUnix()); err != nil {
		writeError(w, f, "database error")
		return
	}
	writeLogin(w, f, token)
}

type historyJSON struct {
	ID           string `json:"id"`
	LastVisit    string `json:"lastvisit"`
	Comments     string `json:"comments"`
	CommentVisit string `json:"commentvisit"`
	Type         string `json:"type"`
}

func writeAccountHistory(w http.ResponseWriter, f format, links []*store.Link) {
	if len(links) == 0 {
		if f == formatXML {
			w.Header().Set("Content-Type", "application/rss+xml")
			_, _ = w.Write([]byte(xmlDoc(`<synccit><links></links></synccit>`)))
		} else {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("[]"))
		}
		return
	}
	if f == formatXML {
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write([]byte(xmlDoc(linksXML(links))))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	out := make([]historyJSON, 0, len(links))
	for _, l := range links {
		j := toLinkJSON(l)
		out = append(out, historyJSON{j.ID, j.LastVisit, j.Comments, j.CommentVisit, "link"})
	}
	_, _ = w.Write([]byte(marshalJSON(out)))
}

type deviceJSON struct {
	Auth      string `json:"auth"`
	Device    string `json:"device"`
	Created   string `json:"created"`
	CreatedBy string `json:"createdby"`
}

func writeDevices(w http.ResponseWriter, f format, codes []*store.AuthCode) {
	if f == formatXML {
		w.Header().Set("Content-Type", "application/rss+xml")
		if len(codes) == 0 {
			_, _ = w.Write([]byte(xmlDoc(`<synccit><devices></devices></synccit>`)))
			return
		}
		var b strings.Builder
		b.WriteString("<synccit><devices>")
		for _, c := range codes {
			createdBy := c.CreatedBy
			if createdBy == "" {
				createdBy = "synccit.com"
			}
			b.WriteString("<device><auth>" + xmlEscape(c.AuthHash) + "</auth><device>" +
				xmlEscape(c.Description) + "</device><created>" + strconv.FormatInt(c.Created, 10) +
				"</created><createdby>" + xmlEscape(createdBy) + "</createdby></device>")
		}
		b.WriteString("</devices></synccit>")
		_, _ = w.Write([]byte(xmlDoc(b.String())))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if len(codes) == 0 {
		_, _ = w.Write([]byte("[]"))
		return
	}
	out := make([]deviceJSON, 0, len(codes))
	for _, c := range codes {
		createdBy := c.CreatedBy
		if createdBy == "" {
			createdBy = "synccit.com"
		}
		out = append(out, deviceJSON{
			Auth:      c.AuthHash,
			Device:    c.Description,
			Created:   strconv.FormatInt(c.Created, 10),
			CreatedBy: createdBy,
		})
	}
	_, _ = w.Write([]byte(marshalJSON(out)))
}

// ---- apps API ----

type appJSON struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Link        string `json:"link"`
	Platform    string `json:"platform"`
}

// handleApps implements apps.php: JSON only, optional platform filter taken
// from the `data` POST field.
func (s *Server) handleApps(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	platform := ""
	if data := r.PostForm.Get("data"); data != "" {
		var payload struct {
			Platform string `json:"platform"`
		}
		if err := json.Unmarshal([]byte(data), &payload); err == nil {
			platform = payload.Platform
		}
	}
	apps, err := s.store.ListApps(platform)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("[]"))
		return
	}
	out := make([]appJSON, 0, len(apps))
	for _, a := range apps {
		out = append(out, appJSON{a.Name, a.Description, a.Link, a.Platform})
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(marshalJSON(out)))
}
