package salesforce

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"kestrel.example/flock/internal/connectors"
)

const (
	scimUserSchema = "urn:ietf:params:scim:schemas:core:2.0:User"
	scimEnterprise = "urn:ietf:params:scim:schemas:extension:enterprise:2.0:User"
	scimPatchOp    = "urn:ietf:params:scim:api:messages:2.0:PatchOp"
	scimPageSize   = 200
)

type scimName struct {
	GivenName  string `json:"givenName,omitempty"`
	FamilyName string `json:"familyName"`
}

type scimValue struct {
	Value   string `json:"value"`
	Type    string `json:"type,omitempty"`
	Primary bool   `json:"primary,omitempty"`
}

type scimEnterpriseExt struct {
	EmployeeNumber string `json:"employeeNumber,omitempty"`
	Department     string `json:"department,omitempty"`
}

// scimUser is the subset of the Salesforce SCIM 2.0 User resource that Flock manages. externalId
// carries the federation id; users without one were not created by Flock and are never modified.
type scimUser struct {
	Schemas      []string           `json:"schemas,omitempty"`
	ID           string             `json:"id,omitempty"`
	UserName     string             `json:"userName"`
	ExternalID   string             `json:"externalId,omitempty"`
	Name         scimName           `json:"name"`
	Emails       []scimValue        `json:"emails,omitempty"`
	Title        string             `json:"title,omitempty"`
	Active       bool               `json:"active"`
	Entitlements []scimValue        `json:"entitlements,omitempty"` // Salesforce: the profile id
	Enterprise   *scimEnterpriseExt `json:"urn:ietf:params:scim:schemas:extension:enterprise:2.0:User,omitempty"`
}

type scimList struct {
	TotalResults int        `json:"totalResults"`
	StartIndex   int        `json:"startIndex"`
	ItemsPerPage int        `json:"itemsPerPage"`
	Resources    []scimUser `json:"Resources"`
}

func toSCIM(a connectors.Account, profileID string) scimUser {
	u := scimUser{
		Schemas:    []string{scimUserSchema, scimEnterprise},
		UserName:   a.UserName,
		ExternalID: a.FederationID,
		Name:       scimName{GivenName: a.GivenName, FamilyName: a.FamilyName},
		Emails:     []scimValue{{Value: a.Email, Type: "work", Primary: true}},
		Title:      a.Title,
		Active:     a.Active,
		Enterprise: &scimEnterpriseExt{EmployeeNumber: a.EmployeeID, Department: a.Department},
	}
	if profileID != "" {
		u.Entitlements = []scimValue{{Value: profileID, Type: "profile"}}
	}
	return u
}

// drifted reports whether the org's copy differs from what Flock wants in a field Flock owns.
func drifted(have, want scimUser) bool {
	if have.Active != want.Active || have.ExternalID != want.ExternalID || have.Title != want.Title ||
		have.Name.GivenName != want.Name.GivenName || have.Name.FamilyName != want.Name.FamilyName {
		return true
	}
	if len(want.Entitlements) > 0 && (len(have.Entitlements) == 0 || have.Entitlements[0].Value != want.Entitlements[0].Value) {
		return true
	}
	return false
}

func (c *Connector) scimURL(suffix string) string {
	return strings.TrimRight(c.tenant.LoginURL, "/") + "/services/scim/v2/Users" + suffix
}

// listUsers pages through every user in the org.
func (c *Connector) listUsers(ctx context.Context) ([]scimUser, error) {
	var all []scimUser
	for start := 1; ; {
		var page scimList
		u := c.scimURL(fmt.Sprintf("?startIndex=%d&count=%d", start, scimPageSize))
		if err := c.do(ctx, http.MethodGet, u, nil, &page); err != nil {
			return nil, err
		}
		all = append(all, page.Resources...)
		if len(page.Resources) == 0 || len(all) >= page.TotalResults {
			return all, nil
		}
		start += len(page.Resources)
	}
}

// findUser returns the user with the given username, or nil.
func (c *Connector) findUser(ctx context.Context, userName string) (*scimUser, error) {
	var page scimList
	filter := url.QueryEscape(fmt.Sprintf("userName eq %q", userName))
	if err := c.do(ctx, http.MethodGet, c.scimURL("?filter="+filter), nil, &page); err != nil {
		return nil, err
	}
	if len(page.Resources) == 0 {
		return nil, nil
	}
	return &page.Resources[0], nil
}

func (c *Connector) createUser(ctx context.Context, u scimUser) error {
	return c.do(ctx, http.MethodPost, c.scimURL(""), u, nil)
}

func (c *Connector) replaceUser(ctx context.Context, id string, u scimUser) error {
	u.ID = id
	return c.do(ctx, http.MethodPut, c.scimURL("/"+id), u, nil)
}

// setActive flips the active flag. Salesforce users cannot be deleted, only deactivated.
func (c *Connector) setActive(ctx context.Context, id string, active bool) error {
	body := map[string]any{
		"schemas":    []string{scimPatchOp},
		"Operations": []map[string]any{{"op": "replace", "path": "active", "value": active}},
	}
	return c.do(ctx, http.MethodPatch, c.scimURL("/"+id), body, nil)
}

// do sends one authenticated JSON request and decodes the response into out (if not nil).
func (c *Connector) do(ctx context.Context, method, u string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return err
	}
	tok, err := c.tokens.Token(ctx)
	if err != nil {
		return fmt.Errorf("salesforce %s: %w", c.tenant.Name, err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("salesforce %s: %s %s: %s: %s", c.tenant.Name, method, req.URL.Path, resp.Status, bytes.TrimSpace(msg))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
