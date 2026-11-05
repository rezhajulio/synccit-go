// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

var (
	benchSinkString string
	benchSinkData   *requestData
	benchSinkBool   bool
)

const benchUpdatePayload = `{"username":"james","auth":"9m89x0","dev":"synccit json","mode":"update","links":[
	{"id":"111111"},
	{"id":"222222","comments":"132"},
	{"id":"333333","comments":313,"both":true},
	{"id":"444444"}
]}`

const benchReadPayload = `{"username":"james","auth":"9m89x0","dev":"synccit json","mode":"read","links":[
	{"id":"111111"},{"id":"222222"},{"id":"333333"},{"id":"444444"}
]}`

func benchHandlerWithLinks(b *testing.B) http.Handler {
	b.Helper()
	st, h := newTestHandler(b, false)
	uid := createUser(b, st, "james", "password123")
	if err := st.InsertAuthCode(uid, "james", "9m89x0", "test device", 1000, "test"); err != nil {
		b.Fatal(err)
	}
	// Seed the four links once, outside the timed section.
	rec := postJSON(b, h, "/api.php", benchUpdatePayload)
	if rec.Code != http.StatusOK {
		b.Fatalf("seed update status = %d", rec.Code)
	}
	return h
}

func benchPost(b *testing.B, h http.Handler, payload string) {
	b.Helper()
	form := url.Values{"data": {payload}, "type": {"json"}}
	body := form.Encode()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api.php", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		benchSinkString = rec.Body.String()
	}
}

// BenchmarkParseUpdateRequest exercises the production JSON request
// parsing (json.Unmarshal into jsonRequest, via parseDataRequest),
// including form decoding — the same overhead on both branches.
func BenchmarkParseUpdateRequest(b *testing.B) {
	form := url.Values{"data": {benchUpdatePayload}, "type": {"json"}}
	body := form.Encode()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api.php", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		_ = req.ParseForm()
		rd, _, _, ok := parseDataRequest(httptest.NewRecorder(), req)
		benchSinkData = rd
		benchSinkBool = ok
	}
}

// BenchmarkFlexLinkUnmarshal isolates one custom UnmarshalJSON call:
// a link object with id/comments/both.
func BenchmarkFlexLinkUnmarshal(b *testing.B) {
	payload := []byte(`{"id":"333333","comments":313,"both":true}`)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var l flexLink
		if err := l.UnmarshalJSON(payload); err != nil {
			b.Fatal(err)
		}
		benchSinkBool = l.input.Both
	}
}

// BenchmarkMarshalHistory100 is the response side: marshal 100 links.
func BenchmarkMarshalHistory100(b *testing.B) {
	links := make([]linkJSON, 0, 100)
	for i := 0; i < 100; i++ {
		links = append(links, linkJSON{
			ID:           fmt.Sprintf("%06d", i),
			LastVisit:    "1700000000",
			Comments:     "42",
			CommentVisit: "1700000100",
		})
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchSinkString = marshalJSON(links)
	}
}

// BenchmarkMarshalSuccess is the small map response (error/success).
func BenchmarkMarshalSuccess(b *testing.B) {
	v := map[string]string{"success": "4 links updated"}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchSinkString = marshalJSON(v)
	}
}

// BenchmarkSyncUpdateHandler is end-to-end: HTTP + JSON parse + SQLite upsert.
func BenchmarkSyncUpdateHandler(b *testing.B) {
	h := benchHandlerWithLinks(b)
	benchPost(b, h, benchUpdatePayload)
}

// BenchmarkSyncReadHandler is end-to-end: HTTP + JSON parse + SQLite read.
func BenchmarkSyncReadHandler(b *testing.B) {
	h := benchHandlerWithLinks(b)
	benchPost(b, h, benchReadPayload)
}
