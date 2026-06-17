package parser

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	shared "github.com/suenot/socials-auto"
)

// --- Happy path: channel snapshot ---

func TestFetchChannel_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/users/937966") {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("site") != "stackoverflow" {
			t.Fatalf("missing site=stackoverflow: %s", r.URL.RawQuery)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"backoff":         0,
			"quota_remaining": 299,
			"items": []map[string]interface{}{{
				"user_id":         937966,
				"display_name":    "Eugen Soloviov",
				"link":            "https://stackoverflow.com/users/937966/eugen-soloviov",
				"reputation":      4242,
				"view_count":      12345,
				"question_count":  17,
				"answer_count":    33,
				"up_vote_count":   500,
				"down_vote_count": 5,
			}},
		})
	}))
	defer srv.Close()

	p := newWithBase(t, srv.URL)
	snap, err := p.FetchChannel(context.Background(), "937966/eugen-soloviov")
	if err != nil {
		t.Fatal(err)
	}
	if snap.Platform != shared.PlatformStackOverflow {
		t.Fatalf("platform: %s", snap.Platform)
	}
	if snap.Followers != 4242 {
		t.Fatalf("followers (reputation) = %d, want 4242", snap.Followers)
	}
	if snap.PostsCount != 50 { // 17 + 33
		t.Fatalf("posts count = %d, want 50", snap.PostsCount)
	}
	if snap.TotalViews != 12345 {
		t.Fatalf("total views = %d, want 12345", snap.TotalViews)
	}
	if snap.TotalLikes != 500 {
		t.Fatalf("total likes = %d, want 500", snap.TotalLikes)
	}
	if snap.Handle != "937966/eugen-soloviov" {
		t.Fatalf("handle preserved = %q", snap.Handle)
	}
	if got := snap.Raw["display_name"]; got != "Eugen Soloviov" {
		t.Fatalf("raw display_name = %v", got)
	}
	if got := snap.Raw["reputation"]; got != int64(4242) {
		t.Fatalf("raw reputation = %v (%T)", got, got)
	}
}

// --- Both endpoints empty -> nil, nil ---

func TestFetchRecentPosts_Empty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"items":[],"quota_remaining":250}`))
	}))
	defer srv.Close()

	p := newWithBase(t, srv.URL)
	posts, err := p.FetchRecentPosts(context.Background(), "937966", time.Time{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if posts != nil {
		t.Fatalf("expected nil slice for empty results, got %d items", len(posts))
	}
}

// --- 404 -> ErrNotFound ---

func TestFetchChannel_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"items":[],"quota_remaining":299}`))
	}))
	defer srv.Close()

	p := newWithBase(t, srv.URL)
	_, err := p.FetchChannel(context.Background(), "1")
	if !errors.Is(err, shared.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

// --- backoff:30 in body -> still returns OK, surfaces in Raw ---

func TestFetchChannel_Backoff(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"backoff":         30,
			"quota_remaining": 10,
			"items": []map[string]interface{}{{
				"user_id":        937966,
				"display_name":   "Eugen Soloviov",
				"reputation":     4000,
				"view_count":     1000,
				"question_count": 1,
				"answer_count":   2,
				"up_vote_count":  10,
			}},
		})
	}))
	defer srv.Close()

	p := newWithBase(t, srv.URL)
	snap, err := p.FetchChannel(context.Background(), "937966")
	if err != nil {
		t.Fatalf("backoff should not be fatal, got err=%v", err)
	}
	if got, ok := snap.Raw["backoff"].(int); !ok || got != 30 {
		t.Fatalf("raw.backoff = %v (%T), want 30", snap.Raw["backoff"], snap.Raw["backoff"])
	}
}

// --- throttle violation (error_id 502) -> ErrRateLimited ---

func TestFetchChannel_ThrottleViolation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error_id":502,"error_name":"throttle_violation","error_message":"too many requests"}`))
	}))
	defer srv.Close()

	p := newWithBase(t, srv.URL)
	_, err := p.FetchChannel(context.Background(), "937966")
	if !errors.Is(err, shared.ErrRateLimited) {
		t.Fatalf("want ErrRateLimited, got %v", err)
	}
}

// --- malformed handle ---

func TestFetchChannel_BadHandle(t *testing.T) {
	p := New(Config{})
	_, err := p.FetchChannel(context.Background(), "not-a-number")
	if err == nil {
		t.Fatalf("expected error for non-numeric handle")
	}
}

// --- Recent posts merge answers + questions, since filter ---

func TestFetchRecentPosts_AnswersAndQuestions(t *testing.T) {
	since := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	old := since.Add(-24 * time.Hour).Unix() // before since
	newer := since.Add(48 * time.Hour).Unix()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/answers"):
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"quota_remaining": 200,
				"items": []map[string]interface{}{
					{
						"answer_id":     1001,
						"question_id":   42,
						"score":         5,
						"creation_date": newer,
						"link":          "https://stackoverflow.com/a/1001",
						"title":         "fresh answer",
					},
					{
						"answer_id":     900,
						"question_id":   41,
						"score":         3,
						"creation_date": old,
						"title":         "stale answer",
					},
				},
			})
		case strings.HasSuffix(r.URL.Path, "/questions"):
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"quota_remaining": 199,
				"items": []map[string]interface{}{
					{
						"question_id":   555,
						"score":         7,
						"view_count":    200,
						"answer_count":  3,
						"creation_date": newer + 1,
						"link":          "https://stackoverflow.com/q/555",
						"title":         "fresh question",
					},
				},
			})
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	p := newWithBase(t, srv.URL)
	posts, err := p.FetchRecentPosts(context.Background(), "937966/eugen-soloviov", since)
	if err != nil {
		t.Fatal(err)
	}
	if len(posts) != 2 {
		t.Fatalf("got %d posts, want 2 (stale answer must be filtered)", len(posts))
	}
	// Newest-first ordering.
	if !posts[0].PublishedAt.After(posts[1].PublishedAt) {
		t.Fatalf("expected newest-first, got %v then %v", posts[0].PublishedAt, posts[1].PublishedAt)
	}
	// First post is the question (newer+1), second is the answer.
	if posts[0].PostID != "q555" {
		t.Fatalf("first post id = %s, want q555", posts[0].PostID)
	}
	if posts[1].PostID != "a1001" {
		t.Fatalf("second post id = %s, want a1001", posts[1].PostID)
	}
	if posts[0].Likes != 7 || posts[0].Views != 200 || posts[0].Comments != 3 {
		t.Fatalf("question fields wrong: %+v", posts[0])
	}
	if posts[1].Likes != 5 {
		t.Fatalf("answer score: %+v", posts[1])
	}
}

// --- AppKey, when present, is forwarded as the "key" query parameter ---

func TestFetchChannel_AppKeyForwarded(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query().Get("key")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"items": []map[string]interface{}{{
				"user_id":    1,
				"reputation": 1,
			}},
		})
	}))
	defer srv.Close()

	p := newWithBaseKey(t, srv.URL, "SECRET")
	_, err := p.FetchChannel(context.Background(), "1")
	if err != nil {
		t.Fatal(err)
	}
	if got != "SECRET" {
		t.Fatalf("key forwarded = %q", got)
	}
}

// --- helpers ---

func newWithBase(t *testing.T, base string) *StackOverflowParser {
	return newWithBaseKey(t, base, "")
}

func newWithBaseKey(t *testing.T, base, key string) *StackOverflowParser {
	t.Helper()
	target := strings.TrimSuffix(base, "/")
	client := &http.Client{
		Transport: rewriteRT{target: target, next: http.DefaultTransport},
		Timeout:   3 * time.Second,
	}
	return New(Config{AppKey: key, HTTPClient: client})
}

type rewriteRT struct {
	target string
	next   http.RoundTripper
}

func (r rewriteRT) RoundTrip(req *http.Request) (*http.Response, error) {
	if strings.HasPrefix(req.URL.String(), apiBase) {
		newURL := r.target + strings.TrimPrefix(req.URL.String(), apiBase)
		req2, err := http.NewRequestWithContext(req.Context(), req.Method, newURL, req.Body)
		if err != nil {
			return nil, err
		}
		req2.Header = req.Header
		return r.next.RoundTrip(req2)
	}
	return r.next.RoundTrip(req)
}
