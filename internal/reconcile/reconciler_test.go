package reconcile

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"kestrel.example/flock/internal/config"
	"kestrel.example/flock/internal/connectors"
	"kestrel.example/flock/internal/lifecycle"
)

type fakeConnector struct {
	tenant string
	calls  []string
}

func (f *fakeConnector) Tenant() string { return f.tenant }
func (f *fakeConnector) Provision(_ context.Context, a connectors.Account) error {
	f.calls = append(f.calls, "provision "+a.UserName)
	return nil
}
func (f *fakeConnector) Deprovision(_ context.Context, a connectors.Account) error {
	f.calls = append(f.calls, "deprovision "+a.UserName)
	return nil
}
func (f *fakeConnector) EnforceSSO(context.Context) error {
	f.calls = append(f.calls, "sso")
	return nil
}
func (f *fakeConnector) Reconcile(_ context.Context, d []connectors.Account) (connectors.Report, error) {
	f.calls = append(f.calls, "reconcile")
	return connectors.Report{Tenant: f.tenant, Unchanged: len(d)}, nil
}

const tenantsYAML = `tenants:
  corp-prod:
    connector: salesforce
    enabled: true
    login_url: https://corp.my.salesforce.com
    service_user: svc.flock@corp.kestrel.example
    enforce_sso_profiles: all
  paused-prod: { connector: salesforce, enabled: false, note: "paused during migration (CHG-0001)" }
  directory:
    connector: workspace
    enabled: true
`

func setup(t *testing.T) (*Reconciler, map[string]*fakeConnector, *bytes.Buffer) {
	t.Helper()
	ts, err := config.ParseTenants([]byte(tenantsYAML))
	if err != nil {
		t.Fatal(err)
	}
	built := map[string]*fakeConnector{}
	var logs bytes.Buffer
	r := &Reconciler{
		Tenants: ts,
		Factory: func(tn config.Tenant) (connectors.Connector, error) {
			if !tn.Enabled {
				t.Errorf("factory called for disabled tenant %s", tn.Name)
			}
			f := &fakeConnector{tenant: tn.Name}
			built[tn.Name] = f
			return f, nil
		},
		Desired: func(context.Context, config.Tenant) ([]connectors.Account, error) {
			return []connectors.Account{{UserName: "test.user1"}}, nil
		},
		Account: func(tn config.Tenant, p lifecycle.Person) connectors.Account {
			return connectors.Account{UserName: p.Email + "/" + tn.Name}
		},
		Logger: slog.New(slog.NewTextHandler(&logs, nil)),
	}
	return r, built, &logs
}

func TestRunOnceSkipsAndLogsDisabledTenants(t *testing.T) {
	r, built, logs := setup(t)
	reports, err := r.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if len(reports) != 2 {
		t.Errorf("reports = %d, want 2", len(reports))
	}
	if _, ok := built["paused-prod"]; ok {
		t.Error("a connector was built for the disabled tenant")
	}
	if got := strings.Join(built["corp-prod"].calls, ","); got != "sso,reconcile" {
		t.Errorf("corp-prod calls = %s", got)
	}
	if got := strings.Join(built["directory"].calls, ","); got != "reconcile" {
		t.Errorf("directory calls = %s (no enforce_sso_profiles, so no sso call)", got)
	}
	out := logs.String()
	if !strings.Contains(out, "tenant disabled: paused-prod") || !strings.Contains(out, "CHG-0001") {
		t.Errorf("log does not report the disabled tenant:\n%s", out)
	}
}

func TestApplyEventsOnlyTouchesEnabledTenants(t *testing.T) {
	r, built, _ := setup(t)
	ev := lifecycle.Event{ID: "hr-7", Type: lifecycle.Leaver, Person: lifecycle.Person{Email: "test.user1@kestrel.example"}}
	if err := r.ApplyEvents(context.Background(), []lifecycle.Event{ev}); err != nil {
		t.Fatal(err)
	}
	if len(built) != 2 || built["paused-prod"] != nil {
		t.Errorf("connectors built for %v", built)
	}
	if got := built["corp-prod"].calls; len(got) != 1 || got[0] != "deprovision test.user1@kestrel.example/corp-prod" {
		t.Errorf("corp-prod calls = %v", got)
	}
}
