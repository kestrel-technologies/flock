// Package slack is a placeholder connector for Slack workspaces that predate Chirp (the Vantage
// Austin workspaces). It is registered so a tenant entry can be parsed and parked, but every
// operation returns connectors.ErrNotSupported; enabling a slack tenant fails at startup.
package slack

import (
	"context"
	"fmt"

	"kestrel.example/flock/internal/config"
	"kestrel.example/flock/internal/connectors"
)

// Connector is the stub.
type Connector struct {
	tenant string
}

var _ connectors.Connector = (*Connector)(nil)

// New returns the stub, or an error if the tenant is enabled.
func New(t config.Tenant) (*Connector, error) {
	if t.Enabled {
		return nil, fmt.Errorf("slack: tenant %s is enabled but the slack connector is not implemented (FLOCK-140)", t.Name)
	}
	return &Connector{tenant: t.Name}, nil
}

func (c *Connector) Tenant() string { return c.tenant }
func (c *Connector) Provision(context.Context, connectors.Account) error {
	return connectors.ErrNotSupported
}
func (c *Connector) Deprovision(context.Context, connectors.Account) error {
	return connectors.ErrNotSupported
}
func (c *Connector) EnforceSSO(context.Context) error { return connectors.ErrNotSupported }
func (c *Connector) Reconcile(context.Context, []connectors.Account) (connectors.Report, error) {
	return connectors.Report{Tenant: c.tenant}, connectors.ErrNotSupported
}
