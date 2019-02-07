// Package workspace is Flock's connector for the Kestrel Workspace directory (mail, docs, calendar).
//
// Workspace authenticates every user through the Kestrel identity provider natively, so EnforceSSO
// has nothing to change. The connector creates, updates and suspends directory users; it never
// deletes them (mailbox retention is handled by Workspace).
package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"kestrel.example/flock/internal/config"
	"kestrel.example/flock/internal/connectors"
)

const managedBy = "flock"

// Options configure a connector.
type Options struct {
	BaseURL    string // directory API, from config/connectors.yaml
	Token      string // API token resolved from Strongbox
	HTTPClient *http.Client
	Logger     *slog.Logger
}

// Connector manages the Workspace directory for one tenant.
type Connector struct {
	tenant config.Tenant
	base   string
	token  string
	http   *http.Client
	log    *slog.Logger
}

var _ connectors.Connector = (*Connector)(nil)

type user struct {
	PrimaryEmail string `json:"primaryEmail"`
	GivenName    string `json:"givenName"`
	FamilyName   string `json:"familyName"`
	Title        string `json:"title,omitempty"`
	Department   string `json:"department,omitempty"`
	EmployeeID   string `json:"employeeId,omitempty"`
	Suspended    bool   `json:"suspended"`
	ManagedBy    string `json:"managedBy,omitempty"`
}

type userPage struct {
	Users         []user `json:"users"`
	NextPageToken string `json:"nextPageToken,omitempty"`
}

var errNotFound = errors.New("workspace: user not found")

// New returns a connector for tenant t.
func New(t config.Tenant, o Options) (*Connector, error) {
	if t.Connector != config.ConnectorWorkspace {
		return nil, fmt.Errorf("workspace: tenant %s uses connector %q", t.Name, t.Connector)
	}
	if o.BaseURL == "" {
		return nil, errors.New("workspace: base_url is required")
	}
	c := &Connector{tenant: t, base: strings.TrimRight(o.BaseURL, "/"), token: o.Token, http: o.HTTPClient, log: o.Logger}
	if c.http == nil {
		c.http = &http.Client{Timeout: 30 * time.Second}
	}
	if c.log == nil {
		c.log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return c, nil
}

// Tenant implements connectors.Connector.
func (c *Connector) Tenant() string { return c.tenant.Name }

func toUser(a connectors.Account) user {
	return user{
		PrimaryEmail: a.Email, GivenName: a.GivenName, FamilyName: a.FamilyName, Title: a.Title,
		Department: a.Department, EmployeeID: a.EmployeeID, Suspended: !a.Active, ManagedBy: managedBy,
	}
}

// Provision implements connectors.Connector.
func (c *Connector) Provision(ctx context.Context, a connectors.Account) error {
	a.Active = true
	want := toUser(a)
	have, err := c.get(ctx, a.Email)
	if errors.Is(err, errNotFound) {
		c.log.Info("provision", "tenant", c.tenant.Name, "user", a.Email)
		return c.do(ctx, http.MethodPost, "/users", want, nil)
	}
	if err != nil {
		return err
	}
	if have == want {
		return nil
	}
	return c.do(ctx, http.MethodPut, "/users/"+url.PathEscape(a.Email), want, nil)
}

// Deprovision implements connectors.Connector: the user is suspended.
func (c *Connector) Deprovision(ctx context.Context, a connectors.Account) error {
	have, err := c.get(ctx, a.Email)
	if errors.Is(err, errNotFound) {
		return nil
	}
	if err != nil || have.Suspended {
		return err
	}
	c.log.Info("deprovision", "tenant", c.tenant.Name, "user", a.Email)
	return c.do(ctx, http.MethodPost, "/users/"+url.PathEscape(a.Email)+":suspend", nil, nil)
}

// EnforceSSO implements connectors.Connector. Workspace sign-in always goes through the Kestrel
// identity provider; there is no per-user setting to change.
func (c *Connector) EnforceSSO(context.Context) error { return nil }

// Reconcile implements connectors.Connector.
func (c *Connector) Reconcile(ctx context.Context, desired []connectors.Account) (connectors.Report, error) {
	rep := connectors.Report{Tenant: c.tenant.Name}
	var existing []user
	for token := ""; ; {
		path := "/users"
		if token != "" {
			path += "?pageToken=" + url.QueryEscape(token)
		}
		var page userPage
		if err := c.do(ctx, http.MethodGet, path, nil, &page); err != nil {
			return rep, err
		}
		existing = append(existing, page.Users...)
		if token = page.NextPageToken; token == "" {
			break
		}
	}
	byEmail := map[string]user{}
	for _, u := range existing {
		byEmail[strings.ToLower(u.PrimaryEmail)] = u
	}
	wanted := map[string]bool{}
	for _, a := range desired {
		key := strings.ToLower(a.Email)
		wanted[key] = true
		want := toUser(a)
		have, ok := byEmail[key]
		var err error
		switch {
		case !ok:
			if err = c.do(ctx, http.MethodPost, "/users", want, nil); err == nil {
				rep.Created++
			}
		case have != want:
			if err = c.do(ctx, http.MethodPut, "/users/"+url.PathEscape(a.Email), want, nil); err == nil {
				rep.Updated++
			}
		default:
			rep.Unchanged++
		}
		if err != nil {
			rep.Errors = append(rep.Errors, fmt.Errorf("%s: %w", a.Email, err))
		}
	}
	for _, u := range existing {
		if u.ManagedBy != managedBy || u.Suspended || wanted[strings.ToLower(u.PrimaryEmail)] {
			continue
		}
		if err := c.do(ctx, http.MethodPost, "/users/"+url.PathEscape(u.PrimaryEmail)+":suspend", nil, nil); err != nil {
			rep.Errors = append(rep.Errors, fmt.Errorf("%s: %w", u.PrimaryEmail, err))
			continue
		}
		rep.Deactivated++
	}
	return rep, nil
}

func (c *Connector) get(ctx context.Context, email string) (user, error) {
	var u user
	err := c.do(ctx, http.MethodGet, "/users/"+url.PathEscape(email), nil, &u)
	return u, err
}

func (c *Connector) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return errNotFound
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("workspace: %s %s: %s", method, path, resp.Status)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
