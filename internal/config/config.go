// Package config loads Flock's tenant and connector configuration.
//
// config/tenants.yaml lists one entry per SaaS tenant that Flock manages. An entry may be written in
// block style or as a one-line flow mapping; both decode to Tenant. config/connectors.yaml holds the
// defaults for each connector type (API versions, Strongbox references, rate limits).
package config

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Connector types Flock knows how to drive.
const (
	ConnectorSalesforce = "salesforce"
	ConnectorWorkspace  = "workspace"
	ConnectorSlack      = "slack"
)

// SSO enforcement modes for enforce_sso_profiles.
const (
	SSOAll  = "all"  // every profile with human users gets single sign-on enforced
	SSONone = "none" // Flock provisions accounts but leaves profile settings alone
)

// Tenant is one managed SaaS tenant.
type Tenant struct {
	Name               string   `yaml:"-"`
	Connector          string   `yaml:"connector"`
	Enabled            bool     `yaml:"enabled"`
	LoginURL           string   `yaml:"login_url,omitempty"`
	ServiceUser        string   `yaml:"service_user,omitempty"`
	EnforceSSOProfiles string   `yaml:"enforce_sso_profiles,omitempty"`
	SSOExemptProfiles  []string `yaml:"sso_exempt_profiles,omitempty"`
	Note               string   `yaml:"note,omitempty"`
}

// EnforcesSSO reports whether the reconciler should call EnforceSSO for this tenant.
func (t Tenant) EnforcesSSO() bool { return t.EnforceSSOProfiles == SSOAll }

// UserDomain is the domain part of the tenant's service user. Provisioned usernames use the same
// domain, e.g. svc.flock@vantage-uat.kestrel.example -> first.last@vantage-uat.kestrel.example.
func (t Tenant) UserDomain() string {
	if i := strings.LastIndex(t.ServiceUser, "@"); i >= 0 {
		return t.ServiceUser[i+1:]
	}
	return ""
}

// Validate checks one tenant entry. Disabled tenants only need a known connector, so an entry can be
// parked as a one-line mapping while it is switched off.
func (t Tenant) Validate() error {
	switch t.Connector {
	case ConnectorSalesforce, ConnectorWorkspace, ConnectorSlack:
	case "":
		return fmt.Errorf("tenant %s: connector is required", t.Name)
	default:
		return fmt.Errorf("tenant %s: unknown connector %q", t.Name, t.Connector)
	}
	switch t.EnforceSSOProfiles {
	case "", SSOAll, SSONone:
	default:
		return fmt.Errorf("tenant %s: enforce_sso_profiles must be %q or %q", t.Name, SSOAll, SSONone)
	}
	if !t.Enabled {
		return nil
	}
	if t.Connector == ConnectorSalesforce {
		if !strings.HasPrefix(t.LoginURL, "https://") {
			return fmt.Errorf("tenant %s: login_url must be an https URL", t.Name)
		}
		if t.UserDomain() == "" {
			return fmt.Errorf("tenant %s: service_user is required for an enabled salesforce tenant", t.Name)
		}
	}
	return nil
}

// Tenants is the parsed tenants.yaml, in file order.
type Tenants struct {
	byName map[string]Tenant
	order  []string
}

type tenantsFile struct {
	Tenants yaml.Node `yaml:"tenants"`
}

// ParseTenants decodes and validates a tenants.yaml document.
func ParseTenants(data []byte) (*Tenants, error) {
	var f tenantsFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("tenants: %w", err)
	}
	if f.Tenants.Kind != yaml.MappingNode {
		return nil, errors.New(`tenants: top-level key "tenants" must be a mapping`)
	}
	ts := &Tenants{byName: map[string]Tenant{}}
	for i := 0; i+1 < len(f.Tenants.Content); i += 2 {
		name := f.Tenants.Content[i].Value
		var t Tenant
		if err := f.Tenants.Content[i+1].Decode(&t); err != nil {
			return nil, fmt.Errorf("tenants: %s: %w", name, err)
		}
		t.Name = name
		if _, dup := ts.byName[name]; dup {
			return nil, fmt.Errorf("tenants: duplicate tenant %q", name)
		}
		if err := t.Validate(); err != nil {
			return nil, err
		}
		ts.byName[name] = t
		ts.order = append(ts.order, name)
	}
	return ts, nil
}

// LoadTenants reads and parses a tenants.yaml file.
func LoadTenants(path string) (*Tenants, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseTenants(data)
}

// Get returns the tenant with the given name.
func (ts *Tenants) Get(name string) (Tenant, bool) {
	t, ok := ts.byName[name]
	return t, ok
}

// All returns every tenant, enabled or not, in file order.
func (ts *Tenants) All() []Tenant {
	out := make([]Tenant, 0, len(ts.order))
	for _, n := range ts.order {
		out = append(out, ts.byName[n])
	}
	return out
}

// Enabled returns the names of the enabled tenants as a set.
func (ts *Tenants) Enabled() map[string]bool {
	out := map[string]bool{}
	for _, t := range ts.byName {
		if t.Enabled {
			out[t.Name] = true
		}
	}
	return out
}

// ConnectorDefaults are the per-connector-type settings from config/connectors.yaml.
type ConnectorDefaults struct {
	Status            string `yaml:"status,omitempty"`
	APIVersion        string `yaml:"api_version,omitempty"`
	BaseURL           string `yaml:"base_url,omitempty"`
	Auth              string `yaml:"auth,omitempty"`
	JWTAudience       string `yaml:"jwt_audience,omitempty"`
	ClientIDRef       string `yaml:"client_id_ref,omitempty"`
	KeyRef            string `yaml:"key_ref,omitempty"`
	TokenRef          string `yaml:"token_ref,omitempty"`
	DefaultProfile    string `yaml:"default_profile,omitempty"`
	RequestsPerMinute int    `yaml:"requests_per_minute,omitempty"`
}

// Connectors maps a connector type to its defaults.
type Connectors map[string]ConnectorDefaults

// LoadConnectors reads config/connectors.yaml.
func LoadConnectors(path string) (Connectors, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f struct {
		Connectors Connectors `yaml:"connectors"`
	}
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("connectors: %w", err)
	}
	if len(f.Connectors) == 0 {
		return nil, errors.New("connectors: no connector types defined")
	}
	return f.Connectors, nil
}

// ExpandRef fills the {tenant} placeholder of a Strongbox reference.
func ExpandRef(ref, tenant string) string {
	return strings.ReplaceAll(ref, "{tenant}", tenant)
}
