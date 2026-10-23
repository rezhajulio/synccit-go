// SPDX-License-Identifier: GPL-3.0-or-later
// Port of synccit by James Wilson / drakeapps (https://github.com/drakeapps/synccit), GPL-3.0-or-later.

package store

import (
	"errors"
	"path/filepath"
	"testing"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestUpsertSemantics(t *testing.T) {
	st := openTestStore(t)
	uid, err := st.CreateUser("james", "hash", "", 100, "test", "")
	if err != nil {
		t.Fatal(err)
	}
	now := int64(1000)

	// Plain link mark.
	if err := st.UpsertLinks(uid, "dev1", "phone", []LinkUpdate{{LinkID: "aaaaaa", MarkLink: true}}, now); err != nil {
		t.Fatal(err)
	}
	// Comments-only update: must not mark the link.
	if err := st.UpsertLinks(uid, "dev1", "phone", []LinkUpdate{{LinkID: "bbbbbb", HasComment: true, CommentCount: 42}}, now); err != nil {
		t.Fatal(err)
	}
	links, err := st.ReadLinks(uid, []string{"aaaaaa", "bbbbbb"})
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 2 {
		t.Fatalf("got %d links, want 2", len(links))
	}
	byID := map[string]*Link{}
	for _, l := range links {
		byID[l.LinkID] = l
	}
	if a := byID["aaaaaa"]; a.LastVisit != now || a.FirstVisit != now || a.LastCommentCount != 0 || a.LastCommentTime != 0 {
		t.Errorf("plain mark wrong: %+v", a)
	}
	if b := byID["bbbbbb"]; b.LastVisit != 0 || b.FirstVisit != 0 || b.LastCommentCount != 42 || b.LastCommentTime != now {
		t.Errorf("comments-only wrong: %+v", b)
	}

	// Later update: lower comment count keeps the max but moves comment
	// time; marking the link now sets lastvisit but not firstvisit's
	// original... firstvisit was 0 for bbbbbb, so it gets set now.
	later := int64(2000)
	if err := st.UpsertLinks(uid, "dev2", "tablet", []LinkUpdate{
		{LinkID: "bbbbbb", HasComment: true, CommentCount: 10, MarkLink: true},
		{LinkID: "aaaaaa", MarkLink: true},
	}, later); err != nil {
		t.Fatal(err)
	}
	links, _ = st.ReadLinks(uid, []string{"aaaaaa", "bbbbbb"})
	for _, l := range links {
		byID[l.LinkID] = l
	}
	if b := byID["bbbbbb"]; b.LastCommentCount != 42 || b.LastCommentTime != later || b.LastVisit != later || b.FirstVisit != later {
		t.Errorf("max/firstvisit semantics wrong: %+v", b)
	}
	if a := byID["aaaaaa"]; a.LastVisit != later || a.FirstVisit != now {
		t.Errorf("firstvisit must be preserved: %+v", a)
	}
	if byID["aaaaaa"].Developers != "dev1, dev2" {
		t.Errorf("developers append wrong: %q", byID["aaaaaa"].Developers)
	}
	if byID["aaaaaa"].LastCall != "tablet" {
		t.Errorf("lastcall wrong: %q", byID["aaaaaa"].LastCall)
	}

	// Short ids are skipped by the insert loop.
	if err := st.UpsertLinks(uid, "dev1", "phone", []LinkUpdate{{LinkID: "abc", MarkLink: true}}, now); err != nil {
		t.Fatal(err)
	}
	links, _ = st.ReadLinks(uid, []string{"abc"})
	if len(links) != 0 {
		t.Errorf("short id was stored: %+v", links)
	}
}

func TestUserUniquenessAndCascade(t *testing.T) {
	st := openTestStore(t)
	uid, err := st.CreateUser("james", "hash", "", 100, "test", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateUser("james", "hash2", "", 100, "test", ""); !IsUniqueViolation(err) {
		t.Fatalf("duplicate create err = %v, want unique violation", err)
	}
	_ = st.InsertAuthCode(uid, "james", "abc123", "phone", 100, "test")
	_ = st.InsertLoginCode(uid, "tok", 100)
	_ = st.UpsertLinks(uid, "dev", "phone", []LinkUpdate{{LinkID: "aaaaaa", MarkLink: true}}, 100)
	if err := st.DeleteUserCascade(uid); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetUserByUsername("james"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("user still exists: %v", err)
	}
	if _, err := st.FindAuthCode("james", "abc123"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("authcode still exists: %v", err)
	}
	links, _ := st.ReadLinks(uid, []string{"aaaaaa"})
	if len(links) != 0 {
		t.Fatalf("links still exist after cascade")
	}
}

func TestHistoryPaging(t *testing.T) {
	st := openTestStore(t)
	uid, _ := st.CreateUser("james", "hash", "", 100, "test", "")
	var updates []LinkUpdate
	for _, id := range []string{"link01", "link02", "link03", "link04", "link05"} {
		updates = append(updates, LinkUpdate{LinkID: id, MarkLink: true})
	}
	if err := st.UpsertLinks(uid, "dev", "phone", updates, 5000); err != nil {
		t.Fatal(err)
	}
	all, err := st.HistoryLinks(uid, 0, 0)
	if err != nil || len(all) != 5 {
		t.Fatalf("history all: %v links, err %v", len(all), err)
	}
	page, _ := st.HistoryLinks(uid, 0, 3)
	if len(page) != 2 {
		t.Fatalf("history offset 3: %d links, want 2", len(page))
	}
	since, _ := st.HistoryLinks(uid, 4000, 0)
	if len(since) != 5 {
		t.Fatalf("history since 4000: %d, want 5", len(since))
	}
	future, _ := st.HistoryLinks(uid, 999999, 0)
	if len(future) != 0 {
		t.Fatalf("history future: %d, want 0", len(future))
	}
}
