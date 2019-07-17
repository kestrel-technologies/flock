package salesforce

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Single sign-on in a Salesforce tenant has two halves. The SAML configuration (issuer
// https://sso.kestrel.example, Flock-issued metadata) is installed once by the tenant's admins when
// the tenant is onboarded; see docs/salesforce-connector.md. Enforcement is what Flock does on every
// reconcile: the profile permission "Is Single Sign-On Enabled" (PermissionsIsSsoEnabled) makes
// users on that profile log in through the Kestrel identity provider.

type profile struct {
	ID         string `json:"Id"`
	Name       string `json:"Name"`
	SSOEnabled bool   `json:"PermissionsIsSsoEnabled"`
	APIOnly    bool   `json:"PermissionsApiUserOnly"`
}

const profileQuery = "SELECT Id, Name, PermissionsIsSsoEnabled, PermissionsApiUserOnly FROM Profile WHERE UserType = 'Standard'"

func (c *Connector) dataURL(suffix string) string {
	return fmt.Sprintf("%s/services/data/v%s%s", strings.TrimRight(c.tenant.LoginURL, "/"), c.apiVersion, suffix)
}

// profiles returns the org's internal-user profiles.
func (c *Connector) profiles(ctx context.Context) ([]profile, error) {
	var res struct {
		Records []profile `json:"records"`
		Done    bool      `json:"done"`
	}
	if err := c.do(ctx, http.MethodGet, c.dataURL("/query?q="+url.QueryEscape(profileQuery)), nil, &res); err != nil {
		return nil, err
	}
	return res.Records, nil
}

// profileID resolves a profile name to its id (cached for the life of the connector).
func (c *Connector) profileID(ctx context.Context, name string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.profileIDs == nil {
		ps, err := c.profiles(ctx)
		if err != nil {
			return "", err
		}
		c.profileIDs = map[string]string{}
		for _, p := range ps {
			c.profileIDs[p.Name] = p.ID
		}
	}
	id, ok := c.profileIDs[name]
	if !ok {
		return "", fmt.Errorf("salesforce %s: profile %q not found", c.tenant.Name, name)
	}
	return id, nil
}

// EnforceSSO turns on "Is Single Sign-On Enabled" for every internal-user profile of the org except
// API-only profiles (service users such as svc.flock cannot complete a SAML login) and the profiles
// listed in the tenant's sso_exempt_profiles (the break-glass profile named in the onboarding CHG).
func (c *Connector) EnforceSSO(ctx context.Context) error {
	ps, err := c.profiles(ctx)
	if err != nil {
		return err
	}
	exempt := map[string]bool{}
	for _, n := range c.tenant.SSOExemptProfiles {
		exempt[n] = true
	}
	var errs []error
	changed := 0
	for _, p := range ps {
		if p.APIOnly || exempt[p.Name] || p.SSOEnabled {
			continue
		}
		body := map[string]bool{"PermissionsIsSsoEnabled": true}
		if err := c.do(ctx, http.MethodPatch, c.dataURL("/sobjects/Profile/"+p.ID), body, nil); err != nil {
			errs = append(errs, fmt.Errorf("profile %s: %w", p.Name, err))
			continue
		}
		changed++
		c.log.Info("sso enforced on profile", "tenant", c.tenant.Name, "profile", p.Name)
	}
	c.log.Info("sso enforcement pass", "tenant", c.tenant.Name, "profiles", len(ps), "changed", changed)
	return errors.Join(errs...)
}
