package salesforce

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"

	"kestrel.example/flock/internal/config"
	"kestrel.example/flock/internal/connectors"
)

// fakeOrg is an in-memory stand-in for the SCIM and REST endpoints the connector uses.
type fakeOrg struct {
	mu       sync.Mutex
	users    []scimUser
	profiles []profile
	patched  []string
	nextID   int
}

var filterRE = regexp.MustCompile(`^userName eq "(.*)"$`)

func (o *fakeOrg) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer test-token" {
		http.Error(w, `[{"errorCode":"INVALID_SESSION_ID"}]`, http.StatusUnauthorized)
		return
	}
	const users = "/services/scim/v2/Users"
	switch {
	case r.URL.Path == users && r.Method == http.MethodGet:
		if m := filterRE.FindStringSubmatch(r.URL.Query().Get("filter")); m != nil {
			var hits []scimUser
			for _, u := range o.users {
				if strings.EqualFold(u.UserName, m[1]) {
					hits = append(hits, u)
				}
			}
			json.NewEncoder(w).Encode(scimList{TotalResults: len(hits), Resources: hits})
			return
		}
		start, _ := strconv.Atoi(r.URL.Query().Get("startIndex"))
		end := min(start-1+2, len(o.users)) // two per page, to exercise paging
		json.NewEncoder(w).Encode(scimList{TotalResults: len(o.users), StartIndex: start, Resources: o.users[start-1 : end]})
	case r.URL.Path == users && r.Method == http.MethodPost:
		var u scimUser
		json.NewDecoder(r.Body).Decode(&u)
		o.nextID++
		u.ID = fmt.Sprintf("005N%d", o.nextID)
		o.users = append(o.users, u)
		w.WriteHeader(http.StatusCreated)
	case strings.HasPrefix(r.URL.Path, users+"/"):
		id := strings.TrimPrefix(r.URL.Path, users+"/")
		for i := range o.users {
			if o.users[i].ID != id {
				continue
			}
			if r.Method == http.MethodPut {
				json.NewDecoder(r.Body).Decode(&o.users[i])
			} else if r.Method == http.MethodPatch {
				var p struct {
					Operations []struct {
						Path  string `json:"path"`
						Value bool   `json:"value"`
					} `json:"Operations"`
				}
				json.NewDecoder(r.Body).Decode(&p)
				o.users[i].Active = p.Operations[0].Value
			}
			return
		}
		http.NotFound(w, r)
	case r.URL.Path == "/services/data/v62.0/query":
		json.NewEncoder(w).Encode(map[string]any{"done": true, "records": o.profiles})
	case strings.HasPrefix(r.URL.Path, "/services/data/v62.0/sobjects/Profile/") && r.Method == http.MethodPatch:
		id := strings.TrimPrefix(r.URL.Path, "/services/data/v62.0/sobjects/Profile/")
		for i := range o.profiles {
			if o.profiles[i].ID == id {
				o.profiles[i].SSOEnabled = true
				o.patched = append(o.patched, o.profiles[i].Name)
			}
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.NotFound(w, r)
	}
}

func (o *fakeOrg) user(name string) *scimUser {
	for i := range o.users {
		if o.users[i].UserName == name {
			return &o.users[i]
		}
	}
	return nil
}

func newTestConnector(t *testing.T, org *fakeOrg, exempt ...string) *Connector {
	t.Helper()
	srv := httptest.NewTLSServer(org) // login_url must be https
	t.Cleanup(srv.Close)
	tenant := config.Tenant{
		Name: "test-prod", Connector: config.ConnectorSalesforce, Enabled: true,
		LoginURL: srv.URL, ServiceUser: "svc.flock@test-prod.kestrel.example",
		EnforceSSOProfiles: config.SSOAll, SSOExemptProfiles: exempt,
	}
	c, err := New(tenant, Options{APIVersion: "62.0", Tokens: StaticToken("test-token"), HTTPClient: srv.Client()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func stdProfiles() []profile {
	return []profile{
		{ID: "00e1", Name: "System Administrator"},
		{ID: "00e2", Name: "Standard User", SSOEnabled: true},
		{ID: "00e3", Name: "Flock Provisioning", APIOnly: true},
		{ID: "00e4", Name: "Emergency Access"},
	}
}

func account(local, title string) connectors.Account {
	return connectors.Account{
		UserName: local + "@test-prod.kestrel.example", Email: local + "@kestrel.example",
		FederationID: local + "@kestrel.example", GivenName: "Test", FamilyName: local, Title: title, Active: true,
	}
}

func TestReconcileCreatesUpdatesAndDeactivates(t *testing.T) {
	org := &fakeOrg{profiles: stdProfiles(), users: []scimUser{
		{ID: "005A", UserName: "svc.flock@test-prod.kestrel.example", Active: true},
		{ID: "005B", UserName: "test.user1@test-prod.kestrel.example", ExternalID: "test.user1@kestrel.example",
			Name: scimName{GivenName: "Test", FamilyName: "test.user1"}, Title: "Analyst", Active: true,
			Entitlements: []scimValue{{Value: "00e2"}}},
		{ID: "005C", UserName: "old.user@test-prod.kestrel.example", ExternalID: "old.user@kestrel.example", Active: true},
		{ID: "005D", UserName: "local.admin@test-prod.kestrel.example", Active: true},
	}}
	c := newTestConnector(t, org)
	rep, err := c.Reconcile(context.Background(), []connectors.Account{
		account("test.user1", "Senior Analyst"),
		account("test.user2", "Engineer"),
	})
	if err != nil || rep.Err() != nil {
		t.Fatalf("Reconcile: %v / %v", err, rep.Err())
	}
	if rep.Created != 1 || rep.Updated != 1 || rep.Deactivated != 1 {
		t.Errorf("report = %+v", rep)
	}
	if u := org.user("test.user1@test-prod.kestrel.example"); u.Title != "Senior Analyst" {
		t.Errorf("test.user1 title = %q", u.Title)
	}
	if u := org.user("test.user2@test-prod.kestrel.example"); u == nil || !u.Active || u.Entitlements[0].Value != "00e2" {
		t.Errorf("test.user2 = %+v", u)
	}
	if org.user("old.user@test-prod.kestrel.example").Active {
		t.Error("old.user should be deactivated")
	}
	// Accounts without a federation id were not created by Flock and must be left alone.
	if !org.user("svc.flock@test-prod.kestrel.example").Active || !org.user("local.admin@test-prod.kestrel.example").Active {
		t.Error("unmanaged accounts were modified")
	}
}

func TestProvisionAndDeprovision(t *testing.T) {
	org := &fakeOrg{profiles: stdProfiles()}
	c := newTestConnector(t, org)
	a := account("test.user3", "Manager")
	if err := c.Provision(context.Background(), a); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if err := c.Provision(context.Background(), a); err != nil || len(org.users) != 1 {
		t.Fatalf("second Provision: %v, users = %d", err, len(org.users))
	}
	if err := c.Deprovision(context.Background(), a); err != nil {
		t.Fatalf("Deprovision: %v", err)
	}
	if org.user(a.UserName).Active {
		t.Error("account still active after Deprovision")
	}
	if err := c.Deprovision(context.Background(), account("nobody", "")); err != nil {
		t.Errorf("Deprovision of a missing account: %v", err)
	}
}

func TestProvisionRefusesForeignFederationID(t *testing.T) {
	org := &fakeOrg{profiles: stdProfiles(), users: []scimUser{
		{ID: "005X", UserName: "test.user3@test-prod.kestrel.example", ExternalID: "other.user@kestrel.example", Active: true},
	}}
	c := newTestConnector(t, org)
	if err := c.Provision(context.Background(), account("test.user3", "")); err == nil {
		t.Fatal("expected an error for a username owned by another federation id")
	}
}

func TestEnforceSSOSkipsAPIOnlyAndExemptProfiles(t *testing.T) {
	org := &fakeOrg{profiles: stdProfiles()}
	c := newTestConnector(t, org, "Emergency Access")
	if err := c.EnforceSSO(context.Background()); err != nil {
		t.Fatalf("EnforceSSO: %v", err)
	}
	if got := strings.Join(org.patched, ","); got != "System Administrator" {
		t.Errorf("patched profiles = %q, want only System Administrator", got)
	}
}

func TestJWTBearerToken(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalPKCS8PrivateKey(key)
	parsed, err := ParseRSAKey(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	if err != nil {
		t.Fatalf("ParseRSAKey: %v", err)
	}
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		r.ParseForm()
		parts := strings.Split(r.PostForm.Get("assertion"), ".")
		if r.URL.Path != "/services/oauth2/token" || len(parts) != 3 {
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
			return
		}
		sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
		sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
		if rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, sum[:], sig) != nil {
			http.Error(w, `{"error":"invalid_grant","error_description":"bad signature"}`, http.StatusBadRequest)
			return
		}
		claims, _ := base64.RawURLEncoding.DecodeString(parts[1])
		if !strings.Contains(string(claims), `"sub":"svc.flock@test-prod.kestrel.example"`) {
			http.Error(w, `{"error":"invalid_grant","error_description":"wrong subject"}`, http.StatusBadRequest)
			return
		}
		w.Write([]byte(`{"access_token":"issued-token","instance_url":"https://test-prod.example"}`))
	}))
	defer srv.Close()
	j := &JWTBearer{LoginURL: srv.URL, ClientID: "test-client", Subject: "svc.flock@test-prod.kestrel.example",
		Audience: "https://login.salesforce.com", Key: parsed, HTTP: srv.Client()}
	for i := 0; i < 2; i++ {
		tok, err := j.Token(context.Background())
		if err != nil || tok != "issued-token" {
			t.Fatalf("Token = %q, %v", tok, err)
		}
	}
	if calls != 1 {
		t.Errorf("token endpoint called %d times, want 1 (cached)", calls)
	}
}
