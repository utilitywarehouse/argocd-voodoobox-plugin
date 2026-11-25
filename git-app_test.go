package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

func testPrivateKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating key: %v", err)
	}

	return key
}

func writeTestKeyFile(t *testing.T, key *rsa.PrivateKey) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "app.pem")
	block := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatalf("writing key: %v", err)
	}

	return path
}

func setGitHubAPIBaseURL(t *testing.T, apiURL string) {
	t.Helper()

	previous := gitHubAPIBaseURL
	gitHubAPIBaseURL = apiURL
	t.Cleanup(func() { gitHubAPIBaseURL = previous })
}

// isolateTokenDir points os.TempDir at a per-test directory so token files do
// not leak between tests.
func isolateTokenDir(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)

	return dir
}

// tokenServer stands in for the access_tokens endpoint and counts requests.
func tokenServer(t *testing.T, token string) (*httptest.Server, *atomic.Int64) {
	t.Helper()

	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusCreated)
		if _, err := fmt.Fprintf(w, `{"token":%q}`, token); err != nil {
			t.Errorf("writing response: %v", err)
		}
	}))
	t.Cleanup(srv.Close)

	return srv, &calls
}

// seedTokenFile writes a token with a modification time age in the past,
// which is how gitAppTokenFile tells how old the token is.
func seedTokenFile(t *testing.T, path, token string, age time.Duration) {
	t.Helper()

	if err := os.WriteFile(path, []byte(token), 0o600); err != nil {
		t.Fatalf("writing token file: %v", err)
	}
	modTime := time.Now().Add(-age)
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatalf("setting token file age: %v", err)
	}
}

func TestGitAppJWT(t *testing.T) {
	key := testPrivateKey(t)
	now := time.Unix(1_700_000_000, 0)

	token, err := gitAppJWT("12345", key, now)
	if err != nil {
		t.Fatalf("gitAppJWT: %v", err)
	}

	parsed, err := jwt.ParseSigned(token, []jose.SignatureAlgorithm{jose.RS256})
	if err != nil {
		t.Fatalf("parsing token: %v", err)
	}
	if got := parsed.Headers[0].Algorithm; got != string(jose.RS256) {
		t.Errorf("alg = %q, want RS256", got)
	}

	var claims jwt.Claims
	if err := parsed.Claims(&key.PublicKey, &claims); err != nil {
		t.Fatalf("verifying signature: %v", err)
	}
	if claims.Issuer != "12345" {
		t.Errorf("iss = %q, want 12345", claims.Issuer)
	}
	if !claims.IssuedAt.Time().Equal(now.Add(-time.Minute)) {
		t.Errorf("iat = %v, want %v", claims.IssuedAt.Time(), now.Add(-time.Minute))
	}
	if !claims.Expiry.Time().Equal(now.Add(9 * time.Minute)) {
		t.Errorf("exp = %v, want %v", claims.Expiry.Time(), now.Add(9*time.Minute))
	}
}

func TestGitAppToken(t *testing.T) {
	key := testPrivateKey(t)
	keyFile := writeTestKeyFile(t, key)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/app/installations/67890/access_tokens" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); !strings.HasPrefix(got, "Bearer ") {
			t.Errorf("authorization = %q, want a bearer token", got)
		}
		if got := r.Header.Get("Accept"); got != "application/vnd.github+json" {
			t.Errorf("accept = %q", got)
		}

		var body struct {
			Permissions map[string]string `json:"permissions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decoding body: %v", err)
		}
		if body.Permissions["contents"] != "read" {
			t.Errorf("permissions = %v, want contents read", body.Permissions)
		}

		w.WriteHeader(http.StatusCreated)
		if _, err := w.Write([]byte(`{"token":"ghs_test"}`)); err != nil {
			t.Errorf("writing response: %v", err)
		}
	}))
	defer srv.Close()

	setGitHubAPIBaseURL(t, srv.URL)

	token, err := gitAppToken(context.Background(), "12345", "67890", keyFile)
	if err != nil {
		t.Fatalf("gitAppToken: %v", err)
	}
	if token != "ghs_test" {
		t.Errorf("token = %q, want ghs_test", token)
	}
}

func TestGitAppToken_ErrorStatus(t *testing.T) {
	keyFile := writeTestKeyFile(t, testPrivateKey(t))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		if _, err := w.Write([]byte(`{"message":"Bad credentials"}`)); err != nil {
			t.Errorf("writing response: %v", err)
		}
	}))
	defer srv.Close()

	setGitHubAPIBaseURL(t, srv.URL)

	_, err := gitAppToken(context.Background(), "12345", "67890", keyFile)
	if err == nil {
		t.Fatal("gitAppToken succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error = %q, want the status in it", err)
	}
}

func TestGitAppTokenFile(t *testing.T) {
	isolateTokenDir(t)

	keyFile := writeTestKeyFile(t, testPrivateKey(t))
	srv, _ := tokenServer(t, "ghs_file")
	setGitHubAPIBaseURL(t, srv.URL)

	path, err := gitAppTokenFile(context.Background(), gitAuth{
		appID:          "12345",
		installationID: "67890",
		appKeyFile:     keyFile,
	})
	if err != nil {
		t.Fatalf("gitAppTokenFile: %v", err)
	}

	want := filepath.Join(os.TempDir(), "argocd-voodoobox-github-app-token")
	if path != want {
		t.Errorf("path = %q, want %q", path, want)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat token file: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", info.Mode().Perm())
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading token file: %v", err)
	}
	if string(content) != "ghs_file" {
		t.Errorf("content = %q, want ghs_file", content)
	}
}

func TestGitAppTokenFile_ReusesToken(t *testing.T) {
	isolateTokenDir(t)

	keyFile := writeTestKeyFile(t, testPrivateKey(t))
	srv, calls := tokenServer(t, "ghs_cached")
	setGitHubAPIBaseURL(t, srv.URL)

	auth := gitAuth{appID: "12345", installationID: "67890", appKeyFile: keyFile}

	for i := 0; i < 3; i++ {
		path, err := gitAppTokenFile(context.Background(), auth)
		if err != nil {
			t.Fatalf("gitAppTokenFile: %v", err)
		}

		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading token file: %v", err)
		}
		if string(content) != "ghs_cached" {
			t.Errorf("content = %q, want ghs_cached", content)
		}
	}

	if got := calls.Load(); got != 1 {
		t.Errorf("token endpoint called %d times, want 1", got)
	}
}

func TestGitAppTokenFile_MintsWhenTokenIsNearExpiry(t *testing.T) {
	isolateTokenDir(t)

	// Inside the refresh margin, so the token is replaced rather than reused.
	path := filepath.Join(os.TempDir(), "argocd-voodoobox-github-app-token")
	seedTokenFile(t, path, "ghs_old", installationTokenLifetime-time.Minute)

	keyFile := writeTestKeyFile(t, testPrivateKey(t))
	srv, calls := tokenServer(t, "ghs_new")
	setGitHubAPIBaseURL(t, srv.URL)

	got, err := gitAppTokenFile(context.Background(), gitAuth{
		appID:          "12345",
		installationID: "67890",
		appKeyFile:     keyFile,
	})
	if err != nil {
		t.Fatalf("gitAppTokenFile: %v", err)
	}
	if got != path {
		t.Errorf("path = %q, want %q", got, path)
	}

	content, err := os.ReadFile(got)
	if err != nil {
		t.Fatalf("reading token file: %v", err)
	}
	if string(content) != "ghs_new" {
		t.Errorf("content = %q, want a freshly minted token", content)
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("token endpoint called %d times, want 1", n)
	}
}

func TestGitAppTokenFile_KeepsUnexpiredTokenOnError(t *testing.T) {
	isolateTokenDir(t)

	path := filepath.Join(os.TempDir(), "argocd-voodoobox-github-app-token")
	seedTokenFile(t, path, "ghs_stale", 58*time.Minute)

	keyFile := writeTestKeyFile(t, testPrivateKey(t))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	setGitHubAPIBaseURL(t, srv.URL)

	if _, err := gitAppTokenFile(context.Background(), gitAuth{
		appID:          "12345",
		installationID: "67890",
		appKeyFile:     keyFile,
	}); err != nil {
		t.Fatalf("gitAppTokenFile: %v", err)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading token file: %v", err)
	}
	if string(content) != "ghs_stale" {
		t.Errorf("content = %q, want the cached token", content)
	}
}

func TestParseGitAppKey_PKCS8(t *testing.T) {
	key := testPrivateKey(t)

	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshalling key: %v", err)
	}

	parsed, err := parseGitAppKey(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	if err != nil {
		t.Fatalf("parseGitAppKey: %v", err)
	}
	if parsed.N.Cmp(key.N) != 0 {
		t.Error("parsed key differs from the generated one")
	}
}

func TestParseGitAppKey_Invalid(t *testing.T) {
	if _, err := parseGitAppKey([]byte("not a key")); err == nil {
		t.Fatal("parseGitAppKey accepted invalid input")
	}
}

func TestGitAuthValidate(t *testing.T) {
	tests := []struct {
		name    string
		auth    gitAuth
		wantErr bool
	}{
		{name: "empty"},
		{name: "app complete", auth: gitAuth{appID: "1", installationID: "2", appKeyFile: "/key"}},
		{name: "token file", auth: gitAuth{tokenFile: "/token"}},
		{name: "app missing key", auth: gitAuth{appID: "1", installationID: "2"}, wantErr: true},
		{name: "app and token file", auth: gitAuth{appID: "1", installationID: "2", appKeyFile: "/key", tokenFile: "/token"}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.auth.validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
