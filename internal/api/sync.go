// SPDX-License-Identifier: GPL-3.0-or-later
// Port of synccit by James Wilson / drakeapps (https://github.com/drakeapps/synccit), GPL-3.0-or-later.

package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/rezhajulio/synccit-go/internal/store"
)

// authIdentity is the result of a successful sync-API checkAuth().
type authIdentity struct {
	userID int64
	device string
}

// checkAuth mirrors the original: try the device auth code first, then fall
// back to the account password (device becomes "none").
func (s *Server) checkAuth(w http.ResponseWriter, username, authStr string, f format) (authIdentity, bool) {
	if ac, err := s.store.FindAuthCode(username, authStr); err == nil {
		return authIdentity{userID: ac.UserID, device: ac.Description}, true
	}
	if u, err := s.store.GetUserByUsername(username); err == nil {
		if s.verifyPassword(u, authStr) {
			return authIdentity{userID: u.ID, device: "none"}, true
		}
	}
	writeError(w, f, "not authorized")
	return authIdentity{}, false
}

// syncAddAuth implements mode=addauth on the sync API: authenticate with the
// account password and mint a new device auth code.
func (s *Server) syncAddAuth(w http.ResponseWriter, req *requestData, f format) {
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
	code, err := s.addAuthCode(u.ID, u.Username, req.Device, req.Dev)
	if err != nil {
		writeError(w, f, "database error")
		return
	}
	writeDevice(w, f, req.Device, code)
}

// buildUpdates folds parsed link inputs into the updates map exactly like the
// PHP code: a present comments value > -1 is a comment update (plus a link
// mark when `both` is set); anything else is a link mark only.
func buildUpdates(links []linkInput) map[string]*store.LinkUpdate {
	updates := make(map[string]*store.LinkUpdate)
	for _, l := range links {
		u, ok := updates[l.ID]
		if !ok {
			u = &store.LinkUpdate{LinkID: l.ID}
			updates[l.ID] = u
		}
		if l.CommentsPresent && l.Comments > -1 {
			u.HasComment = true
			u.CommentCount = l.Comments
			if l.Both {
				u.MarkLink = true
			}
		} else {
			u.MarkLink = true
		}
	}
	return updates
}

func updatesSlice(updates map[string]*store.LinkUpdate) []store.LinkUpdate {
	out := make([]store.LinkUpdate, 0, len(updates))
	for _, u := range updates {
		out = append(out, *u)
	}
	return out
}

type linkJSON struct {
	ID           string `json:"id"`
	LastVisit    string `json:"lastvisit"`
	Comments     string `json:"comments"`
	CommentVisit string `json:"commentvisit"`
}

func toLinkJSON(l *store.Link) linkJSON {
	return linkJSON{
		ID:           l.LinkID,
		LastVisit:    strconv.FormatInt(l.LastVisit, 10),
		Comments:     strconv.FormatInt(l.LastCommentCount, 10),
		CommentVisit: strconv.FormatInt(l.LastCommentTime, 10),
	}
}

func linksXML(links []*store.Link) string {
	var b strings.Builder
	b.WriteString("<synccit><links>")
	for _, l := range links {
		j := toLinkJSON(l)
		b.WriteString("<link><id>" + xmlEscape(j.ID) + "</id><lastvisit>" + j.LastVisit +
			"</lastvisit><comments>" + j.Comments + "</comments><commentvisit>" + j.CommentVisit +
			"</commentvisit></link>")
	}
	b.WriteString("</links></synccit>")
	return b.String()
}

// writeLinks writes read/history output in the requested format, including
// the original empty-result behavior per format.
func writeLinks(w http.ResponseWriter, f format, links []*store.Link) {
	if len(links) == 0 {
		switch f {
		case formatJSON:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("[]"))
		case formatXML:
			w.Header().Set("Content-Type", "application/rss+xml")
			_, _ = w.Write([]byte(xmlDoc(`<synccit><links></links></synccit>`)))
		default:
			writeError(w, formatText, "no links found")
		}
		return
	}
	switch f {
	case formatJSON:
		w.Header().Set("Content-Type", "application/json")
		out := make([]linkJSON, 0, len(links))
		for _, l := range links {
			out = append(out, toLinkJSON(l))
		}
		_, _ = w.Write([]byte(marshalJSON(out)))
	case formatXML:
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write([]byte(xmlDoc(linksXML(links))))
	default:
		var b strings.Builder
		for _, l := range links {
			fmt.Fprintf(&b, "%s:%d;%d:%d,\n", l.LinkID, l.LastVisit, l.LastCommentCount, l.LastCommentTime)
		}
		_, _ = w.Write([]byte(b.String()))
	}
}

func (s *Server) handleSync(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-API", "1")
	w.Header().Set("X-Revision", "12")

	req, f, hasData, ok := parseDataRequest(w, r)
	if !ok {
		return
	}
	if !hasData {
		s.handleSyncPlain(w, r)
		return
	}

	switch req.Mode {
	case "create":
		s.createAccount(w, r, req, f)
		return
	case "addauth":
		s.syncAddAuth(w, req, f)
		return
	}

	identity, authed := s.checkAuth(w, req.Username, req.Auth, f)
	if !authed {
		return
	}

	switch req.Mode {
	case "update":
		updates := buildUpdates(req.Links)
		if err := s.store.UpsertLinks(identity.userID, req.Dev, identity.device, updatesSlice(updates), nowUnix()); err != nil {
			writeError(w, f, "database error")
			return
		}
		// The original counts map entries, including ids the insert loop
		// skips for being too short.
		writeSuccess(w, f, fmt.Sprintf("%d links updated", len(updates)))
	case "history":
		links, err := s.store.HistoryLinks(identity.userID, req.Time, req.Offset)
		if err != nil {
			writeError(w, f, "database error")
			return
		}
		writeLinks(w, f, links)
	default: // "read" and anything else
		if len(req.Links) == 0 {
			writeError(w, f, "no links requested")
			return
		}
		ids := make([]string, 0, len(req.Links))
		for _, l := range req.Links {
			ids = append(ids, l.ID)
		}
		links, err := s.store.ReadLinks(identity.userID, ids)
		if err != nil {
			writeError(w, f, "database error")
			return
		}
		writeLinks(w, f, links)
	}
}

// splitPlainList mirrors the PHP plaintext parsing: a value without a comma
// is a one-element list (even when empty).
func splitPlainList(v string) []string {
	if !strings.Contains(v, ",") {
		return []string{v}
	}
	return strings.Split(v, ",")
}

func (s *Server) handleSyncPlain(w http.ResponseWriter, r *http.Request) {
	_, hasUser := r.PostForm["username"]
	_, hasAuth := r.PostForm["auth"]
	mode := r.PostForm.Get("mode")
	if !hasUser || !hasAuth || mode == "" {
		writeError(w, formatText, "no post data")
		return
	}
	username := r.PostForm.Get("username")
	authStr := r.PostForm.Get("auth")
	dev := "unknown"
	if _, ok := r.PostForm["dev"]; ok {
		dev = r.PostForm.Get("dev")
	}

	identity, authed := s.checkAuth(w, username, authStr, formatText)
	if !authed {
		return
	}

	if mode == "update" {
		updates := make(map[string]*store.LinkUpdate)
		for _, id := range splitPlainList(r.PostForm.Get("links")) {
			u, ok := updates[id]
			if !ok {
				u = &store.LinkUpdate{LinkID: id}
				updates[id] = u
			}
			u.MarkLink = true
		}
		if _, present := r.PostForm["comments"]; present {
			for _, c := range splitPlainList(r.PostForm.Get("comments")) {
				parts := strings.Split(c, ":")
				if len(parts) != 2 {
					continue
				}
				u, ok := updates[parts[0]]
				if !ok {
					u = &store.LinkUpdate{LinkID: parts[0]}
					updates[parts[0]] = u
				}
				n, _ := strconv.ParseInt(parts[1], 10, 64)
				u.HasComment = true
				u.CommentCount = n
			}
		}
		if err := s.store.UpsertLinks(identity.userID, dev, identity.device, updatesSlice(updates), nowUnix()); err != nil {
			writeError(w, formatText, "database error")
			return
		}
		writeSuccess(w, formatText, fmt.Sprintf("%d links updated", len(updates)))
		return
	}

	ids := splitPlainList(r.PostForm.Get("links"))
	links, err := s.store.ReadLinks(identity.userID, ids)
	if err != nil {
		writeError(w, formatText, "database error")
		return
	}
	writeLinks(w, formatText, links)
}
