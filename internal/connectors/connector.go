// Package connectors defines the interface every SaaS connector implements and the account model
// shared between them. One connector instance serves exactly one tenant from config/tenants.yaml.
package connectors

import (
	"context"
	"errors"
	"strings"

	"kestrel.example/flock/internal/lifecycle"
)

// Account is a user account in a tenant, as Flock wants it to be.
type Account struct {
	UserName     string // tenant username, e.g. test.user1@vantage-uat.kestrel.example
	Email        string // corporate email
	FederationID string // SAML subject; always the corporate email
	EmployeeID   string
	GivenName    string
	FamilyName   string
	Title        string
	Department   string
	Profile      string // tenant-specific role or profile name; empty means the connector default
	Active       bool
}

// Report summarises one reconcile pass over a tenant.
type Report struct {
	Tenant      string
	Created     int
	Updated     int
	Deactivated int
	Unchanged   int
	Errors      []error
}

// Err joins the per-account errors of the pass.
func (r Report) Err() error { return errors.Join(r.Errors...) }

// Connector drives one SaaS tenant.
type Connector interface {
	// Tenant is the tenant name from config/tenants.yaml.
	Tenant() string
	// Provision creates the account, or reactivates and updates it if it exists.
	Provision(ctx context.Context, a Account) error
	// Deprovision deactivates the account. A missing account is not an error.
	Deprovision(ctx context.Context, a Account) error
	// EnforceSSO makes single sign-on through the Kestrel identity provider mandatory for human
	// users of the tenant.
	EnforceSSO(ctx context.Context) error
	// Reconcile makes the tenant's Flock-managed accounts match desired: missing accounts are
	// created, drifted ones updated, and managed accounts that are not desired are deactivated.
	Reconcile(ctx context.Context, desired []Account) (Report, error)
}

// ErrNotSupported is returned by connectors that do not implement an operation.
var ErrNotSupported = errors.New("connectors: operation not supported by this connector")

// AccountFor maps an HR record to the account Flock manages in a tenant whose usernames live under
// userDomain (the domain of the tenant's service user).
func AccountFor(p lifecycle.Person, userDomain, profile string) Account {
	local := p.Email
	if i := strings.LastIndex(local, "@"); i >= 0 {
		local = local[:i]
	}
	return Account{
		UserName:     strings.ToLower(local) + "@" + userDomain,
		Email:        strings.ToLower(p.Email),
		FederationID: strings.ToLower(p.Email),
		EmployeeID:   p.EmployeeID,
		GivenName:    p.GivenName,
		FamilyName:   p.FamilyName,
		Title:        p.Title,
		Department:   p.Department,
		Profile:      profile,
		Active:       true,
	}
}
