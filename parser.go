// Package parser implements the w_popularity stackoverflow adapter.
//
// Status: STUB. Returns shared.ErrNotImplemented.
//
// Strategy:
//   primary:  Stack Exchange API
//   fallback: none
package parser

import (
	"context"
	"time"

	shared "github.com/suenot/w-popularity-shared"
)

// Config controls runtime behaviour. Add platform-specific fields here.
type Config struct {
	// Token, cookie, or API key — fill in per implementation.
	Credential string
	// HTTPTimeout caps every outbound call.
	HTTPTimeout time.Duration
	// CamoufoxURL is set when falling back to browser-based scraping.
	CamoufoxURL string
}

// New constructs a stubbed parser. Real impl is pending.
func New(cfg Config) *StackOverflowParser { return &StackOverflowParser{cfg: cfg} }

type StackOverflowParser struct{ cfg Config }

func (p *StackOverflowParser) Platform() shared.Platform { return shared.PlatformStackOverflow }

func (p *StackOverflowParser) FetchChannel(ctx context.Context, handle string) (shared.ChannelSnapshot, error) {
	return shared.ChannelSnapshot{}, shared.ErrNotImplemented
}

func (p *StackOverflowParser) FetchRecentPosts(ctx context.Context, handle string, since time.Time) ([]shared.PostSnapshot, error) {
	return nil, shared.ErrNotImplemented
}
