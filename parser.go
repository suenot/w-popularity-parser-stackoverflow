// Package parser implements the w_popularity stackoverflow adapter using the
// Stack Exchange API v2.3.
//
// Endpoints used (all GET, JSON):
//   - /users/{id}?site=stackoverflow
//   - /users/{id}/answers?site=stackoverflow&order=desc&sort=creation&pagesize=50
//   - /users/{id}/questions?site=stackoverflow&order=desc&sort=creation&pagesize=50
//
// No authentication required. An optional Config.AppKey ("key" query parameter)
// raises the daily quota from 300 to 10000 requests.
//
// Handle format used by w_popularity: "<userid>/<slug>" e.g. "937966/eugen-soloviov".
// Only the leading numeric id is sent to the API; the slug is human-readable
// and ignored for API calls.
package parser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	shared "github.com/suenot/socials-auto"
)

const apiBase = "https://api.stackexchange.com/2.3"

const siteParam = "stackoverflow"

// userFilter is a Stack Exchange custom filter that extends the `default`
// filter with the user-totals fields the API otherwise hides
// (view_count, question_count, answer_count, up_vote_count, down_vote_count)
// plus envelope diagnostics (backoff, quota, error_*).
//
// Generated once via:
//
//	GET /2.3/filters/create?base=default
//	    &include=user.question_count;user.answer_count;user.up_vote_count;
//	             user.down_vote_count;user.view_count;
//	             .has_more;.quota_remaining;.quota_max;
//	             .backoff;.error_id;.error_name;.error_message
//
// Filter IDs are stable for the lifetime of the API.
const userFilter = "!)RL-JogHwoZuazwo6-n_WuMc"

// Config controls runtime behaviour.
type Config struct {
	// AppKey is the optional "key" query parameter that boosts quota to 10k/day.
	// Leave empty to use the 300/day anonymous quota.
	AppKey string
	// HTTPClient lets tests inject a custom transport.
	HTTPClient *http.Client
	// HTTPTimeout caps every outbound call. Default 15s.
	HTTPTimeout time.Duration
}

// New constructs a StackOverflowParser ready for use.
func New(cfg Config) *StackOverflowParser {
	if cfg.HTTPClient == nil {
		t := cfg.HTTPTimeout
		if t == 0 {
			t = 15 * time.Second
		}
		cfg.HTTPClient = &http.Client{Timeout: t}
	}
	return &StackOverflowParser{cfg: cfg}
}

type StackOverflowParser struct{ cfg Config }

func (p *StackOverflowParser) Platform() shared.Platform { return shared.PlatformStackOverflow }

// --- Channel ---

type seEnvelope struct {
	Backoff        int    `json:"backoff"`
	QuotaRemaining int    `json:"quota_remaining"`
	QuotaMax       int    `json:"quota_max"`
	HasMore        bool   `json:"has_more"`
	ErrorID        int    `json:"error_id"`
	ErrorName      string `json:"error_name"`
	ErrorMessage   string `json:"error_message"`
}

type userItem struct {
	UserID         int64  `json:"user_id"`
	DisplayName    string `json:"display_name"`
	Link           string `json:"link"`
	Reputation     int64  `json:"reputation"`
	ViewCount      int64  `json:"view_count"`
	QuestionCount  int64  `json:"question_count"`
	AnswerCount    int64  `json:"answer_count"`
	UpVoteCount    int64  `json:"up_vote_count"`
	DownVoteCount  int64  `json:"down_vote_count"`
	// Profile metadata. These are part of the default filter shape on
	// Stack Exchange and are populated when the user opts to expose them.
	CreationDate    int64  `json:"creation_date"`
	LastAccessDate  int64  `json:"last_access_date"`
	LastModifiedDate int64 `json:"last_modified_date"`
	Location        string `json:"location"`
	WebsiteURL      string `json:"website_url"`
	ProfileImage    string `json:"profile_image"`
	AccountID       int64  `json:"account_id"`
	UserType        string `json:"user_type"`
	BadgeCounts     struct {
		Bronze int `json:"bronze"`
		Silver int `json:"silver"`
		Gold   int `json:"gold"`
	} `json:"badge_counts"`
	IsEmployee  bool `json:"is_employee"`
	AcceptRate  int  `json:"accept_rate"`
}

type userResp struct {
	seEnvelope
	Items []userItem `json:"items"`
}

// FetchChannel returns one snapshot for the user identified by handle.
//
// handle may be either a bare numeric id ("937966") or the canonical
// "<userid>/<slug>" form used in w_popularity. The slug is ignored.
func (p *StackOverflowParser) FetchChannel(ctx context.Context, handle string) (shared.ChannelSnapshot, error) {
	uid, slug, err := parseHandle(handle)
	if err != nil {
		return shared.ChannelSnapshot{}, err
	}

	q := url.Values{}
	q.Set("site", siteParam)
	q.Set("filter", userFilter)
	p.addKey(q)

	var resp userResp
	if err := p.getJSON(ctx, fmt.Sprintf("%s/users/%d?%s", apiBase, uid, q.Encode()), &resp); err != nil {
		return shared.ChannelSnapshot{}, err
	}
	if err := envelopeError(&resp.seEnvelope); err != nil {
		return shared.ChannelSnapshot{}, err
	}
	logEnvelope("users", &resp.seEnvelope)
	if len(resp.Items) == 0 {
		return shared.ChannelSnapshot{}, fmt.Errorf("stackoverflow: %w", shared.ErrNotFound)
	}
	it := resp.Items[0]

	link := it.Link
	if link == "" {
		link = fmt.Sprintf("https://stackoverflow.com/users/%d", uid)
	}

	raw := map[string]interface{}{
		"user_id":         it.UserID,
		"display_name":    it.DisplayName,
		"reputation":      it.Reputation,
		"question_count":  it.QuestionCount,
		"answer_count":    it.AnswerCount,
		"up_vote_count":   it.UpVoteCount,
		"down_vote_count": it.DownVoteCount,
		"quota_remaining": resp.QuotaRemaining,
		"badges": map[string]int{
			"gold":   it.BadgeCounts.Gold,
			"silver": it.BadgeCounts.Silver,
			"bronze": it.BadgeCounts.Bronze,
		},
	}
	if it.Location != "" {
		raw["location"] = it.Location
	}
	if it.WebsiteURL != "" {
		raw["website_url"] = it.WebsiteURL
	}
	if it.ProfileImage != "" {
		raw["profile_image"] = it.ProfileImage
	}
	if it.UserType != "" {
		raw["user_type"] = it.UserType
	}
	if it.AccountID != 0 {
		raw["account_id"] = it.AccountID
	}
	if it.CreationDate != 0 {
		raw["creation_date"] = it.CreationDate
	}
	if it.LastAccessDate != 0 {
		raw["last_access_date"] = it.LastAccessDate
	}
	if it.LastModifiedDate != 0 {
		raw["last_modified_date"] = it.LastModifiedDate
	}
	if it.AcceptRate != 0 {
		raw["accept_rate"] = it.AcceptRate
	}
	if it.IsEmployee {
		raw["is_employee"] = true
	}
	if resp.Backoff > 0 {
		raw["backoff"] = resp.Backoff
	}
	if slug != "" {
		raw["slug"] = slug
	}

	return shared.ChannelSnapshot{
		Platform:      shared.PlatformStackOverflow,
		Handle:        handle,
		URL:           link,
		FetchedAt:     time.Now().UTC(),
		Followers:     it.Reputation,
		PostsCount:    it.QuestionCount + it.AnswerCount,
		TotalLikes:    it.UpVoteCount,
		TotalViews:    it.ViewCount,
		TotalComments: 0,
		Raw:           raw,
	}, nil
}

// --- Posts (answers + questions) ---

type answerItem struct {
	AnswerID     int64 `json:"answer_id"`
	QuestionID   int64 `json:"question_id"`
	Score        int64 `json:"score"`
	IsAccepted   bool  `json:"is_accepted"`
	CreationDate int64 `json:"creation_date"`
	Link         string `json:"link"`
	Title        string `json:"title"`
}

type answersResp struct {
	seEnvelope
	Items []answerItem `json:"items"`
}

type questionItem struct {
	QuestionID   int64  `json:"question_id"`
	Score        int64  `json:"score"`
	ViewCount    int64  `json:"view_count"`
	AnswerCount  int64  `json:"answer_count"`
	CreationDate int64  `json:"creation_date"`
	Link         string `json:"link"`
	Title        string `json:"title"`
}

type questionsResp struct {
	seEnvelope
	Items []questionItem `json:"items"`
}

// FetchRecentPosts returns answers + questions authored by user `handle`
// whose creation_date >= since. Newest-first.
func (p *StackOverflowParser) FetchRecentPosts(ctx context.Context, handle string, since time.Time) ([]shared.PostSnapshot, error) {
	uid, _, err := parseHandle(handle)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()

	// Answers.
	aq := url.Values{}
	aq.Set("site", siteParam)
	aq.Set("order", "desc")
	aq.Set("sort", "creation")
	aq.Set("pagesize", "50")
	p.addKey(aq)

	var ar answersResp
	if err := p.getJSON(ctx, fmt.Sprintf("%s/users/%d/answers?%s", apiBase, uid, aq.Encode()), &ar); err != nil {
		return nil, err
	}
	if err := envelopeError(&ar.seEnvelope); err != nil {
		return nil, err
	}
	logEnvelope("answers", &ar.seEnvelope)

	// Questions.
	qq := url.Values{}
	qq.Set("site", siteParam)
	qq.Set("order", "desc")
	qq.Set("sort", "creation")
	qq.Set("pagesize", "50")
	p.addKey(qq)

	var qr questionsResp
	if err := p.getJSON(ctx, fmt.Sprintf("%s/users/%d/questions?%s", apiBase, uid, qq.Encode()), &qr); err != nil {
		return nil, err
	}
	if err := envelopeError(&qr.seEnvelope); err != nil {
		return nil, err
	}
	logEnvelope("questions", &qr.seEnvelope)

	if len(ar.Items) == 0 && len(qr.Items) == 0 {
		return nil, nil
	}

	out := make([]shared.PostSnapshot, 0, len(ar.Items)+len(qr.Items))

	for _, a := range ar.Items {
		published := time.Unix(a.CreationDate, 0).UTC()
		if !since.IsZero() && published.Before(since) {
			continue
		}
		link := a.Link
		if link == "" && a.QuestionID > 0 {
			link = fmt.Sprintf("https://stackoverflow.com/a/%d", a.AnswerID)
		}
		out = append(out, shared.PostSnapshot{
			Platform:      shared.PlatformStackOverflow,
			ChannelHandle: handle,
			PostID:        fmt.Sprintf("a%d", a.AnswerID),
			URL:           link,
			Kind:          shared.PostKindPost,
			PublishedAt:   published,
			FetchedAt:     now,
			Likes:         a.Score,
			Views:         0,
			Comments:      0,
			Raw: map[string]interface{}{
				"type":        "answer",
				"answer_id":   a.AnswerID,
				"question_id": a.QuestionID,
				"is_accepted": a.IsAccepted,
				"title":       a.Title,
			},
		})
	}

	for _, q := range qr.Items {
		published := time.Unix(q.CreationDate, 0).UTC()
		if !since.IsZero() && published.Before(since) {
			continue
		}
		link := q.Link
		if link == "" {
			link = fmt.Sprintf("https://stackoverflow.com/q/%d", q.QuestionID)
		}
		out = append(out, shared.PostSnapshot{
			Platform:      shared.PlatformStackOverflow,
			ChannelHandle: handle,
			PostID:        fmt.Sprintf("q%d", q.QuestionID),
			URL:           link,
			Kind:          shared.PostKindPost,
			PublishedAt:   published,
			FetchedAt:     now,
			Likes:         q.Score,
			Views:         q.ViewCount,
			Comments:      q.AnswerCount,
			Raw: map[string]interface{}{
				"type":         "question",
				"question_id":  q.QuestionID,
				"answer_count": q.AnswerCount,
				"title":        q.Title,
			},
		})
	}

	// Sort newest-first by PublishedAt without pulling in sort to keep
	// dependencies small — n is at most ~100 so an O(n^2) selection is fine.
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].PublishedAt.After(out[i].PublishedAt) {
				out[i], out[j] = out[j], out[i]
			}
		}
	}

	return out, nil
}

// --- HTTP helpers ---

func (p *StackOverflowParser) addKey(q url.Values) {
	if p.cfg.AppKey != "" {
		q.Set("key", p.cfg.AppKey)
	}
}

func (p *StackOverflowParser) getJSON(ctx context.Context, u string, dst interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	// Stack Exchange responses are gzipped; net/http auto-decompresses when
	// we don't set Accept-Encoding ourselves.
	resp, err := p.cfg.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("stackoverflow: %w: %v", shared.ErrTransient, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("stackoverflow: %w: %v", shared.ErrTransient, err)
	}

	if resp.StatusCode >= 500 {
		return fmt.Errorf("stackoverflow: %w: http %d", shared.ErrTransient, resp.StatusCode)
	}
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("stackoverflow: %w", shared.ErrNotFound)
	}

	if err := json.Unmarshal(body, dst); err != nil {
		return fmt.Errorf("stackoverflow: decode: %w", err)
	}
	return nil
}

// envelopeError maps Stack Exchange error_id values onto sentinel errors.
//
// Reference: https://api.stackexchange.com/docs/error-handling
//
//	error_id 502 ("throttle_violation")  -> ErrRateLimited
//	other 4xx-style ids                  -> generic error
func envelopeError(e *seEnvelope) error {
	if e.ErrorID == 0 {
		return nil
	}
	switch e.ErrorID {
	case 502:
		return fmt.Errorf("stackoverflow: %w: %s", shared.ErrRateLimited, e.ErrorMessage)
	case 401, 403:
		return fmt.Errorf("stackoverflow: %w: %s", shared.ErrAuth, e.ErrorMessage)
	case 404:
		return fmt.Errorf("stackoverflow: %w", shared.ErrNotFound)
	case 500:
		return fmt.Errorf("stackoverflow: %w: %s", shared.ErrTransient, e.ErrorMessage)
	default:
		return fmt.Errorf("stackoverflow: api error %d (%s): %s", e.ErrorID, e.ErrorName, e.ErrorMessage)
	}
}

// logEnvelope prints quota + backoff info at debug level.
func logEnvelope(tag string, e *seEnvelope) {
	if e.Backoff > 0 {
		log.Printf("stackoverflow[%s]: backoff=%ds quota_remaining=%d", tag, e.Backoff, e.QuotaRemaining)
		return
	}
	if e.QuotaRemaining > 0 && e.QuotaRemaining < 20 {
		log.Printf("stackoverflow[%s]: quota_remaining=%d (low)", tag, e.QuotaRemaining)
	}
}

// parseHandle accepts "<userid>" or "<userid>/<slug>" and returns the numeric id.
func parseHandle(handle string) (int64, string, error) {
	if handle == "" {
		return 0, "", fmt.Errorf("stackoverflow: empty handle")
	}
	parts := strings.SplitN(handle, "/", 2)
	idStr := strings.TrimSpace(parts[0])
	if idStr == "" {
		return 0, "", fmt.Errorf("stackoverflow: handle missing user id")
	}
	uid, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		return 0, "", fmt.Errorf("stackoverflow: invalid user id %q: %w", idStr, err)
	}
	slug := ""
	if len(parts) == 2 {
		slug = strings.TrimSpace(parts[1])
	}
	return uid, slug, nil
}

// Compile-time interface check.
var _ shared.Parser = (*StackOverflowParser)(nil)

// keep errors import used even if sentinel set shrinks.
var _ = errors.Is
