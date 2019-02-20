package workspace

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"kestrel.example/flock/internal/config"
	"kestrel.example/flock/internal/connectors"
)

type fakeDirectory struct {
	mu    sync.Mutex
	users map[string]*user
	order []string
}

func (d *fakeDirectory) add(u user) {
	d.users[u.PrimaryEmail] = &u
	d.order = append(d.order, u.PrimaryEmail)
}

func (d *fakeDirectory) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	d.mu.Lock()
	defer d.mu.Unlock()
	switch {
	case r.URL.Path == "/users" && r.Method == http.MethodGet:
		// one user per page, to exercise paging
		i := 0
		if tok := r.URL.Query().Get("pageToken"); tok != "" {
			for i < len(d.order) && d.order[i] != tok {
				i++
			}
		}
		page := userPage{}
		if i < len(d.order) {
			page.Users = []user{*d.users[d.order[i]]}
		}
		if i+1 < len(d.order) {
			page.NextPageToken = d.order[i+1]
		}
		json.NewEncoder(w).Encode(page)
	case r.URL.Path == "/users" && r.Method == http.MethodPost:
		var u user
		json.NewDecoder(r.Body).Decode(&u)
		d.add(u)
	case strings.HasSuffix(r.URL.Path, ":suspend"):
		email := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/users/"), ":suspend")
		if u, ok := d.users[email]; ok {
			u.Suspended = true
		}
	case strings.HasPrefix(r.URL.Path, "/users/"):
		email := strings.TrimPrefix(r.URL.Path, "/users/")
		u, ok := d.users[email]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if r.Method == http.MethodPut {
			json.NewDecoder(r.Body).Decode(u)
			return
		}
		json.NewEncoder(w).Encode(u)
	default:
		http.NotFound(w, r)
	}
}

func newConnector(t *testing.T, d *fakeDirectory) *Connector {
	srv := httptest.NewServer(d)
	t.Cleanup(srv.Close)
	c, err := New(config.Tenant{Name: "directory", Connector: config.ConnectorWorkspace, Enabled: true},
		Options{BaseURL: srv.URL, Token: "test-token", HTTPClient: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func acct(email, title string) connectors.Account {
	return connectors.Account{Email: email, GivenName: "T", FamilyName: "U", Title: title, Active: true}
}

func TestReconcile(t *testing.T) {
	d := &fakeDirectory{users: map[string]*user{}}
	d.add(user{PrimaryEmail: "test.user1@kestrel.example", GivenName: "T", FamilyName: "U", Title: "Analyst", ManagedBy: "flock"})
	d.add(user{PrimaryEmail: "gone.user@kestrel.example", ManagedBy: "flock"})
	d.add(user{PrimaryEmail: "conference-room-4@kestrel.example"})
	c := newConnector(t, d)
	rep, err := c.Reconcile(context.Background(), []connectors.Account{
		acct("test.user1@kestrel.example", "Senior Analyst"),
		acct("test.user2@kestrel.example", "Engineer"),
	})
	if err != nil || rep.Err() != nil {
		t.Fatalf("Reconcile: %v / %v", err, rep.Err())
	}
	if rep.Created != 1 || rep.Updated != 1 || rep.Deactivated != 1 {
		t.Errorf("report = %+v", rep)
	}
	if !d.users["gone.user@kestrel.example"].Suspended {
		t.Error("gone.user not suspended")
	}
	if d.users["conference-room-4@kestrel.example"].Suspended {
		t.Error("unmanaged resource account was suspended")
	}
}

func TestProvisionDeprovision(t *testing.T) {
	d := &fakeDirectory{users: map[string]*user{}}
	c := newConnector(t, d)
	a := acct("test.user3@kestrel.example", "Manager")
	if err := c.Provision(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	if err := c.Deprovision(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	if !d.users[a.Email].Suspended {
		t.Error("not suspended")
	}
	if err := c.Deprovision(context.Background(), acct("nobody@kestrel.example", "")); err != nil {
		t.Errorf("missing user: %v", err)
	}
	if err := c.EnforceSSO(context.Background()); err != nil {
		t.Errorf("EnforceSSO: %v", err)
	}
}
