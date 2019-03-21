package config

import (
	"path/filepath"
	"strings"
	"testing"
)

const sample = `tenants:
  alpha-prod:
    connector: salesforce
    enabled: true
    login_url: https://alpha.my.salesforce.com
    service_user: svc.flock@alpha.kestrel.example
    enforce_sso_profiles: all
  beta-prod: { connector: salesforce, enabled: false, note: "paused for migration" }
  directory:
    connector: workspace
    enabled: true
`

func TestParseTenantsBlockAndFlowStyle(t *testing.T) {
	ts, err := ParseTenants([]byte(sample))
	if err != nil {
		t.Fatalf("ParseTenants: %v", err)
	}
	var names []string
	for _, tn := range ts.All() {
		names = append(names, tn.Name)
	}
	if got := strings.Join(names, ","); got != "alpha-prod,beta-prod,directory" {
		t.Fatalf("file order = %s", got)
	}
	alpha, _ := ts.Get("alpha-prod")
	if !alpha.Enabled || !alpha.EnforcesSSO() || alpha.UserDomain() != "alpha.kestrel.example" {
		t.Errorf("alpha-prod decoded as %+v", alpha)
	}
	beta, ok := ts.Get("beta-prod")
	if !ok || beta.Enabled || beta.Connector != ConnectorSalesforce || beta.Note != "paused for migration" {
		t.Errorf("beta-prod (flow style) decoded as %+v", beta)
	}
	if en := ts.Enabled(); len(en) != 2 || !en["alpha-prod"] || !en["directory"] {
		t.Errorf("Enabled() = %v", en)
	}
}

func TestEnabledSalesforceTenantNeedsLoginAndServiceUser(t *testing.T) {
	cases := map[string]string{
		"no login_url":    "tenants:\n  x: { connector: salesforce, enabled: true, service_user: svc.flock@x.kestrel.example }\n",
		"http login_url":  "tenants:\n  x: { connector: salesforce, enabled: true, login_url: http://x, service_user: svc.flock@x.kestrel.example }\n",
		"no service_user": "tenants:\n  x: { connector: salesforce, enabled: true, login_url: https://x.my.salesforce.com }\n",
	}
	for name, src := range cases {
		if _, err := ParseTenants([]byte(src)); err == nil {
			t.Errorf("%s: expected a validation error", name)
		}
	}
	// The same entry is accepted while the tenant is disabled.
	if _, err := ParseTenants([]byte("tenants:\n  x: { connector: salesforce, enabled: false }\n")); err != nil {
		t.Errorf("disabled partial entry rejected: %v", err)
	}
}

func TestRejectsUnknownConnectorAndSSOMode(t *testing.T) {
	for _, src := range []string{
		"tenants:\n  x: { connector: ldap, enabled: false }\n",
		"tenants:\n  x: { enabled: false }\n",
		"tenants:\n  x: { connector: workspace, enabled: true, enforce_sso_profiles: some }\n",
		"tenant:\n  x: { connector: workspace }\n",
	} {
		if _, err := ParseTenants([]byte(src)); err == nil {
			t.Errorf("expected an error for %q", src)
		}
	}
}

func TestLoadConnectors(t *testing.T) {
	cs, err := LoadConnectors(filepath.Join("..", "..", "config", "connectors.yaml"))
	if err != nil {
		t.Fatalf("LoadConnectors: %v", err)
	}
	sf, ok := cs[ConnectorSalesforce]
	if !ok || sf.APIVersion == "" || !strings.HasPrefix(sf.KeyRef, "strongbox://") {
		t.Errorf("salesforce defaults = %+v", sf)
	}
	if got := ExpandRef(sf.KeyRef, "alpha-prod"); strings.Contains(got, "{tenant}") || !strings.Contains(got, "alpha-prod") {
		t.Errorf("ExpandRef = %s", got)
	}
}
