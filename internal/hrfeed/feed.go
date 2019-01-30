// Package hrfeed reads the People Systems HR feed: the current roster and a cursor-based stream of
// joiner / mover / leaver events.
package hrfeed

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"kestrel.example/flock/internal/lifecycle"
)

// Client talks to the HR feed API.
type Client struct {
	BaseURL string // e.g. https://people.kestrel.example/feed
	Token   string // bearer token, resolved from Strongbox by the caller
	HTTP    *http.Client
}

// EventPage is one page of events and the cursor to pass next time.
type EventPage struct {
	Events []lifecycle.Event `json:"events"`
	Cursor string            `json:"cursor"`
}

// Roster returns every active worker.
func (c *Client) Roster(ctx context.Context) ([]lifecycle.Person, error) {
	var out struct {
		People []lifecycle.Person `json:"people"`
	}
	if err := c.get(ctx, "/v2/roster", nil, &out); err != nil {
		return nil, err
	}
	return out.People, nil
}

// Events returns the events after cursor (empty cursor: from the start of the retention window).
func (c *Client) Events(ctx context.Context, cursor string) (EventPage, error) {
	q := url.Values{}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	var page EventPage
	err := c.get(ctx, "/v2/events", q, &page)
	return page, err
}

func (c *Client) get(ctx context.Context, path string, q url.Values, out any) error {
	u := strings.TrimRight(c.BaseURL, "/") + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	var lastErr error
	for attempt := 0; attempt < 4; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt*attempt) * 250 * time.Millisecond):
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Accept", "application/json")
		if c.Token != "" {
			req.Header.Set("Authorization", "Bearer "+c.Token)
		}
		resp, err := hc.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			resp.Body.Close()
			lastErr = fmt.Errorf("hrfeed: GET %s: %s", path, resp.Status)
			continue
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("hrfeed: GET %s: %s", path, resp.Status)
		}
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return lastErr
}
