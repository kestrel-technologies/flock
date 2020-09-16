package hrfeed

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"kestrel.example/flock/internal/lifecycle"
)

func TestRosterAndEvents(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/v2/roster":
			w.Write([]byte(`{"people":[{"employee_id":"E100","email":"test.user1@kestrel.example","entitlements":["corp-prod"]}]}`))
		case "/v2/events":
			if calls.Add(1) == 1 {
				http.Error(w, "slow down", http.StatusTooManyRequests)
				return
			}
			if r.URL.Query().Get("cursor") != "c1" {
				t.Errorf("cursor = %q", r.URL.Query().Get("cursor"))
			}
			w.Write([]byte(`{"cursor":"c2","events":[{"id":"hr-9","type":"leaver","person":{"email":"test.user1@kestrel.example"}}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL, Token: "test-token", HTTP: srv.Client()}
	people, err := c.Roster(context.Background())
	if err != nil || len(people) != 1 || !people[0].Entitled("corp-prod") {
		t.Fatalf("Roster = %+v, %v", people, err)
	}
	page, err := c.Events(context.Background(), "c1")
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	if page.Cursor != "c2" || len(page.Events) != 1 || page.Events[0].Type != lifecycle.Leaver {
		t.Errorf("Events = %+v", page)
	}
}

func TestNonRetryableError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, HTTP: srv.Client()}
	if _, err := c.Roster(context.Background()); err == nil {
		t.Fatal("expected an error on 403")
	}
}
