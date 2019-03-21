// Package salesforce is Flock's connector for Salesforce orgs.
//
// Users are managed through the org's SCIM 2.0 endpoint (/services/scim/v2/Users), profile settings
// through the REST API, and Flock authenticates as the tenant's service user (svc.flock) with the
// OAuth 2.0 JWT bearer flow. Only users whose externalId (federation id) was set by Flock are ever
// changed; service users and break-glass accounts created by the org's admins are left alone.
package salesforce

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"kestrel.example/flock/internal/config"
	"kestrel.example/flock/internal/connectors"
)

// Options configure a connector. Zero values fall back to defaults.
type Options struct {
	APIVersion     string // e.g. "62.0", from config/connectors.yaml
	DefaultProfile string // profile for accounts that do not name one
	Tokens         TokenSource
	HTTPClient     *http.Client
	Logger         *slog.Logger
}

// Connector manages one Salesforce tenant.
type Connector struct {
	tenant         config.Tenant
	apiVersion     string
	defaultProfile string
	tokens         TokenSource
	http           *http.Client
	log            *slog.Logger

	mu         sync.Mutex
	profileIDs map[string]string
}

var _ connectors.Connector = (*Connector)(nil)

// New returns a connector for tenant t.
func New(t config.Tenant, o Options) (*Connector, error) {
	if t.Connector != config.ConnectorSalesforce {
		return nil, fmt.Errorf("salesforce: tenant %s uses connector %q", t.Name, t.Connector)
	}
	if err := t.Validate(); err != nil {
		return nil, err
	}
	if o.Tokens == nil {
		return nil, errors.New("salesforce: no token source")
	}
	c := &Connector{
		tenant:         t,
		apiVersion:     o.APIVersion,
		defaultProfile: o.DefaultProfile,
		tokens:         o.Tokens,
		http:           o.HTTPClient,
		log:            o.Logger,
	}
	if c.apiVersion == "" {
		c.apiVersion = "62.0"
	}
	if c.defaultProfile == "" {
		c.defaultProfile = "Standard User"
	}
	if c.http == nil {
		c.http = &http.Client{Timeout: 60 * time.Second}
	}
	if c.log == nil {
		c.log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return c, nil
}

// Tenant implements connectors.Connector.
func (c *Connector) Tenant() string { return c.tenant.Name }

func (c *Connector) desired(ctx context.Context, a connectors.Account) (scimUser, error) {
	name := a.Profile
	if name == "" {
		name = c.defaultProfile
	}
	id, err := c.profileID(ctx, name)
	if err != nil {
		return scimUser{}, err
	}
	return toSCIM(a, id), nil
}

// Provision implements connectors.Connector.
func (c *Connector) Provision(ctx context.Context, a connectors.Account) error {
	a.Active = true
	want, err := c.desired(ctx, a)
	if err != nil {
		return err
	}
	have, err := c.findUser(ctx, a.UserName)
	if err != nil {
		return err
	}
	if have == nil {
		c.log.Info("provision", "tenant", c.tenant.Name, "user", a.UserName)
		return c.createUser(ctx, want)
	}
	if have.ExternalID != "" && !strings.EqualFold(have.ExternalID, a.FederationID) {
		return fmt.Errorf("salesforce %s: %s belongs to federation id %s", c.tenant.Name, a.UserName, have.ExternalID)
	}
	if !drifted(*have, want) {
		return nil
	}
	c.log.Info("update", "tenant", c.tenant.Name, "user", a.UserName)
	return c.replaceUser(ctx, have.ID, want)
}

// Deprovision implements connectors.Connector.
func (c *Connector) Deprovision(ctx context.Context, a connectors.Account) error {
	have, err := c.findUser(ctx, a.UserName)
	if err != nil || have == nil || !have.Active {
		return err
	}
	c.log.Info("deprovision", "tenant", c.tenant.Name, "user", a.UserName)
	return c.setActive(ctx, have.ID, false)
}

// Reconcile implements connectors.Connector.
func (c *Connector) Reconcile(ctx context.Context, desired []connectors.Account) (connectors.Report, error) {
	rep := connectors.Report{Tenant: c.tenant.Name}
	existing, err := c.listUsers(ctx)
	if err != nil {
		return rep, err
	}
	byName := make(map[string]scimUser, len(existing))
	for _, u := range existing {
		byName[strings.ToLower(u.UserName)] = u
	}
	wanted := make(map[string]bool, len(desired))
	for _, a := range desired {
		key := strings.ToLower(a.UserName)
		wanted[key] = true
		want, err := c.desired(ctx, a)
		if err != nil {
			rep.Errors = append(rep.Errors, err)
			continue
		}
		have, ok := byName[key]
		switch {
		case !ok:
			err = c.createUser(ctx, want)
			if err == nil {
				rep.Created++
			}
		case drifted(have, want):
			err = c.replaceUser(ctx, have.ID, want)
			if err == nil {
				rep.Updated++
			}
		default:
			rep.Unchanged++
		}
		if err != nil {
			rep.Errors = append(rep.Errors, fmt.Errorf("%s: %w", a.UserName, err))
		}
	}
	for _, u := range existing {
		if u.ExternalID == "" || !u.Active || wanted[strings.ToLower(u.UserName)] {
			continue
		}
		if err := c.setActive(ctx, u.ID, false); err != nil {
			rep.Errors = append(rep.Errors, fmt.Errorf("%s: %w", u.UserName, err))
			continue
		}
		rep.Deactivated++
	}
	c.log.Info("reconciled", "tenant", c.tenant.Name, "created", rep.Created, "updated", rep.Updated,
		"deactivated", rep.Deactivated, "unchanged", rep.Unchanged, "errors", len(rep.Errors))
	return rep, nil
}
