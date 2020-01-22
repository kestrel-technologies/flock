// Package reconcile runs Flock's control loop: apply HR events as they arrive and, on every
// interval, reconcile each enabled tenant against the HR roster.
package reconcile

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"kestrel.example/flock/internal/config"
	"kestrel.example/flock/internal/connectors"
	"kestrel.example/flock/internal/lifecycle"
)

// Factory builds the connector for an enabled tenant.
type Factory func(t config.Tenant) (connectors.Connector, error)

// DesiredFunc returns the accounts that should exist in a tenant.
type DesiredFunc func(ctx context.Context, t config.Tenant) ([]connectors.Account, error)

// AccountFunc maps an HR record to the tenant's account for event-driven actions.
type AccountFunc func(t config.Tenant, p lifecycle.Person) connectors.Account

// Reconciler is the control loop. Tenants with enabled: false are skipped on every pass and logged
// as "tenant disabled: <name>"; no connector is built for them and their accounts are not touched.
type Reconciler struct {
	Tenants  *config.Tenants
	Factory  Factory
	Desired  DesiredFunc
	Account  AccountFunc
	Interval time.Duration
	Logger   *slog.Logger

	conns map[string]connectors.Connector
}

func (r *Reconciler) logger() *slog.Logger {
	if r.Logger == nil {
		r.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return r.Logger
}

func (r *Reconciler) connector(t config.Tenant) (connectors.Connector, error) {
	if r.conns == nil {
		r.conns = map[string]connectors.Connector{}
	}
	if c, ok := r.conns[t.Name]; ok {
		return c, nil
	}
	c, err := r.Factory(t)
	if err != nil {
		return nil, fmt.Errorf("tenant %s: %w", t.Name, err)
	}
	r.conns[t.Name] = c
	return c, nil
}

func (r *Reconciler) logDisabled(t config.Tenant) {
	attrs := []any{"tenant", t.Name, "connector", t.Connector}
	if t.Note != "" {
		attrs = append(attrs, "note", t.Note)
	}
	r.logger().Info("tenant disabled: "+t.Name, attrs...)
}

// RunOnce reconciles every enabled tenant once and returns one report per reconciled tenant.
func (r *Reconciler) RunOnce(ctx context.Context) ([]connectors.Report, error) {
	var reports []connectors.Report
	var errs []error
	for _, t := range r.Tenants.All() {
		if !t.Enabled {
			r.logDisabled(t)
			continue
		}
		c, err := r.connector(t)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if t.EnforcesSSO() {
			if err := c.EnforceSSO(ctx); err != nil {
				errs = append(errs, fmt.Errorf("tenant %s: enforce sso: %w", t.Name, err))
			}
		}
		desired, err := r.Desired(ctx, t)
		if err != nil {
			errs = append(errs, fmt.Errorf("tenant %s: desired state: %w", t.Name, err))
			continue
		}
		rep, err := c.Reconcile(ctx, desired)
		if err != nil {
			errs = append(errs, fmt.Errorf("tenant %s: reconcile: %w", t.Name, err))
			continue
		}
		if rep.Err() != nil {
			errs = append(errs, fmt.Errorf("tenant %s: %w", t.Name, rep.Err()))
		}
		reports = append(reports, rep)
	}
	return reports, errors.Join(errs...)
}

// ApplyEvents plans and applies HR events against the enabled tenants.
func (r *Reconciler) ApplyEvents(ctx context.Context, events []lifecycle.Event) error {
	var errs []error
	for _, ev := range events {
		actions, err := lifecycle.Plan(ev, r.Tenants.Enabled())
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, a := range actions {
			t, _ := r.Tenants.Get(a.Tenant)
			c, err := r.connector(t)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			acct := r.Account(t, a.Person)
			switch a.Kind {
			case lifecycle.Provision, lifecycle.Update:
				err = c.Provision(ctx, acct)
			case lifecycle.Deprovision:
				err = c.Deprovision(ctx, acct)
			}
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", a, err))
				continue
			}
			r.logger().Info("applied", "action", a.String())
		}
	}
	return errors.Join(errs...)
}

// Run reconciles immediately and then every Interval until ctx is cancelled.
func (r *Reconciler) Run(ctx context.Context) error {
	interval := r.Interval
	if interval <= 0 {
		interval = 15 * time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		start := time.Now()
		reports, err := r.RunOnce(ctx)
		if err != nil {
			r.logger().Error("reconcile pass finished with errors", "err", err)
		}
		r.logger().Info("reconcile pass", "tenants", len(reports), "took", time.Since(start).Round(time.Millisecond))
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
