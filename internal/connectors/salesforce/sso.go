package salesforce

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"kestrel.example/flock/internal/connectors"
)

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

// EnforceSSO implements connectors.Connector. Profile enforcement lands with FLOCK-33.
func (c *Connector) EnforceSSO(context.Context) error { return connectors.ErrNotSupported }
