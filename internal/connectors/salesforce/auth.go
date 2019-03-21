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
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// TokenSource returns an OAuth access token for the tenant's service user.
type TokenSource interface {
	Token(ctx context.Context) (string, error)
}

// StaticToken is a TokenSource for tests and dry runs.
type StaticToken string

// Token implements TokenSource.
func (s StaticToken) Token(context.Context) (string, error) { return string(s), nil }

// JWTBearer implements the OAuth 2.0 JWT bearer flow: Flock signs an assertion for the tenant's
// service user (svc.flock) with the connected app's private key and exchanges it for an access
// token. No password and no refresh token is involved. The key and the consumer key come from
// Strongbox; see docs/salesforce-connector.md.
type JWTBearer struct {
	LoginURL string // the tenant's My Domain URL
	ClientID string // connected app consumer key
	Subject  string // service user, e.g. svc.flock@vantage-uat.kestrel.example
	Audience string // https://login.salesforce.com (production) or https://test.salesforce.com (sandbox)
	Key      *rsa.PrivateKey
	HTTP     *http.Client

	mu      sync.Mutex
	token   string
	expires time.Time
}

// tokenLifetime is how long Flock reuses an access token. The org's session timeout is longer.
const tokenLifetime = 30 * time.Minute

// Token implements TokenSource.
func (j *JWTBearer) Token(ctx context.Context) (string, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.token != "" && time.Now().Before(j.expires) {
		return j.token, nil
	}
	assertion, err := j.assertion(time.Now())
	if err != nil {
		return "", err
	}
	form := url.Values{
		"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"},
		"assertion":  {assertion},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(j.LoginURL, "/")+"/services/oauth2/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	hc := j.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var body struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("salesforce: token response: %w", err)
	}
	if resp.StatusCode != http.StatusOK || body.AccessToken == "" {
		return "", fmt.Errorf("salesforce: token request for %s failed: %s %s", j.Subject, body.Error, body.Description)
	}
	j.token, j.expires = body.AccessToken, time.Now().Add(tokenLifetime)
	return j.token, nil
}

func (j *JWTBearer) assertion(now time.Time) (string, error) {
	if j.Key == nil {
		return "", errors.New("salesforce: no signing key")
	}
	enc := base64.RawURLEncoding
	header := enc.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims, err := json.Marshal(map[string]any{
		"iss": j.ClientID,
		"sub": j.Subject,
		"aud": j.Audience,
		"exp": now.Add(3 * time.Minute).Unix(),
	})
	if err != nil {
		return "", err
	}
	signingInput := header + "." + enc.EncodeToString(claims)
	sum := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, j.Key, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return signingInput + "." + enc.EncodeToString(sig), nil
}

// ParseRSAKey decodes a PEM-encoded RSA private key (PKCS#8 or PKCS#1) as stored in Strongbox.
func ParseRSAKey(data []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("salesforce: signing key is not PEM")
	}
	if k, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		rk, ok := k.(*rsa.PrivateKey)
		if !ok {
			return nil, errors.New("salesforce: signing key is not RSA")
		}
		return rk, nil
	}
	return x509.ParsePKCS1PrivateKey(block.Bytes)
}
