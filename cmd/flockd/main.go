// Command flockd is the Flock daemon: it loads config/tenants.yaml and config/connectors.yaml,
// applies HR events as they arrive and reconciles every enabled tenant on a fixed interval.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"kestrel.example/flock/internal/config"
	"kestrel.example/flock/internal/connectors"
	"kestrel.example/flock/internal/connectors/salesforce"
	"kestrel.example/flock/internal/connectors/slack"
	"kestrel.example/flock/internal/connectors/workspace"
	"kestrel.example/flock/internal/hrfeed"
	"kestrel.example/flock/internal/lifecycle"
	"kestrel.example/flock/internal/reconcile"
	"kestrel.example/flock/internal/secrets"
)

// corporateDomain is the username domain for tenants without a service user (Workspace).
const corporateDomain = "kestrel.example"

func main() {
	tenantsPath := flag.String("tenants", "config/tenants.yaml", "tenant configuration")
	connectorsPath := flag.String("connectors", "config/connectors.yaml", "connector defaults")
	interval := flag.Duration("interval", 15*time.Minute, "full reconcile interval")
	eventInterval := flag.Duration("event-interval", time.Minute, "HR event polling interval")
	flag.Parse()

	log := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "flockd")
	if err := run(log, *tenantsPath, *connectorsPath, *interval, *eventInterval); err != nil {
		log.Error("flockd stopped", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger, tenantsPath, connectorsPath string, interval, eventInterval time.Duration) error {
	tenants, err := config.LoadTenants(tenantsPath)
	if err != nil {
		return err
	}
	conns, err := config.LoadConnectors(connectorsPath)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	vault := secrets.FileResolver{Root: env("STRONGBOX_ROOT", "/var/run/strongbox")}
	feedToken, err := vault.Resolve(ctx, env("FLOCK_HR_FEED_TOKEN_REF", "strongbox://entsec-identity/flock/hr-feed/token"))
	if err != nil {
		return err
	}
	feed := &hrfeed.Client{BaseURL: env("FLOCK_HR_FEED_URL", "https://people.kestrel.example/feed"), Token: string(feedToken)}

	r := &reconcile.Reconciler{
		Tenants:  tenants,
		Factory:  factory(ctx, conns, vault, log),
		Desired:  desired(feed, conns),
		Account:  func(t config.Tenant, p lifecycle.Person) connectors.Account { return account(t, p, conns) },
		Interval: interval,
		Logger:   log,
	}
	for _, t := range tenants.All() {
		log.Info("tenant loaded", "tenant", t.Name, "connector", t.Connector, "enabled", t.Enabled)
	}

	go pollEvents(ctx, log, feed, r, eventInterval)
	return r.Run(ctx)
}

// pollEvents applies HR events. The cursor lives in memory: after a restart the feed replays its
// retention window, which is safe because every connector operation is idempotent.
func pollEvents(ctx context.Context, log *slog.Logger, feed *hrfeed.Client, r *reconcile.Reconciler, every time.Duration) {
	cursor := ""
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		page, err := feed.Events(ctx, cursor)
		if err != nil {
			log.Error("hr feed", "err", err)
		} else {
			if err := r.ApplyEvents(ctx, page.Events); err != nil {
				log.Error("apply hr events", "err", err)
			}
			cursor = page.Cursor
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func factory(ctx context.Context, conns config.Connectors, vault secrets.Resolver, log *slog.Logger) reconcile.Factory {
	return func(t config.Tenant) (connectors.Connector, error) {
		d := conns[t.Connector]
		switch t.Connector {
		case config.ConnectorSalesforce:
			clientID, err := vault.Resolve(ctx, config.ExpandRef(d.ClientIDRef, t.Name))
			if err != nil {
				return nil, err
			}
			keyPEM, err := vault.Resolve(ctx, config.ExpandRef(d.KeyRef, t.Name))
			if err != nil {
				return nil, err
			}
			key, err := salesforce.ParseRSAKey(keyPEM)
			if err != nil {
				return nil, err
			}
			tokens := &salesforce.JWTBearer{LoginURL: t.LoginURL, ClientID: string(clientID), Subject: t.ServiceUser,
				Audience: d.JWTAudience, Key: key}
			return salesforce.New(t, salesforce.Options{APIVersion: d.APIVersion, DefaultProfile: d.DefaultProfile,
				Tokens: tokens, Logger: log})
		case config.ConnectorWorkspace:
			token, err := vault.Resolve(ctx, config.ExpandRef(d.TokenRef, t.Name))
			if err != nil {
				return nil, err
			}
			return workspace.New(t, workspace.Options{BaseURL: d.BaseURL, Token: string(token), Logger: log})
		case config.ConnectorSlack:
			return slack.New(t)
		}
		return nil, fmt.Errorf("tenant %s: no connector %q", t.Name, t.Connector)
	}
}

func account(t config.Tenant, p lifecycle.Person, conns config.Connectors) connectors.Account {
	domain := t.UserDomain()
	if domain == "" {
		domain = corporateDomain
	}
	return connectors.AccountFor(p, domain, conns[t.Connector].DefaultProfile)
}

func desired(feed *hrfeed.Client, conns config.Connectors) reconcile.DesiredFunc {
	return func(ctx context.Context, t config.Tenant) ([]connectors.Account, error) {
		people, err := feed.Roster(ctx)
		if err != nil {
			return nil, err
		}
		var out []connectors.Account
		for _, p := range people {
			if p.Entitled(t.Name) {
				out = append(out, account(t, p, conns))
			}
		}
		return out, nil
	}
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
