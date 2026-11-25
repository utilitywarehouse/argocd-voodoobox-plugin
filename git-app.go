package main

import (
	"bytes"
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

// installationTokenLifetime is how long GitHub accepts an installation token.
const installationTokenLifetime = time.Hour

// gitAppTokenRefreshMargin is how much of that lifetime a cached token keeps
// unused, so a build never starts with a token that dies mid-fetch.
const gitAppTokenRefreshMargin = 5 * time.Minute

// gitHubAPIBaseURL is a variable so tests can point the token exchange at a
// local server.
var gitHubAPIBaseURL = "https://api.github.com"

// gitAppTokenFile returns the path of a file holding an installation token,
// minting a new one only when the cached token is close to expiry.
//
// The file is the cache: git's credential helper reads it on every call, so a
// build gets a token without the plugin copying it anywhere. Installation
// tokens live for an hour, which the file's modification time records.
func gitAppTokenFile(ctx context.Context, auth gitAuth) (string, error) {
	path := filepath.Join(os.TempDir(), "argocd-voodoobox-github-app-token")

	// A missing or unreadable file is as stale as a token can be.
	age := installationTokenLifetime
	if info, err := os.Stat(path); err == nil {
		age = time.Since(info.ModTime())
	}
	if age < installationTokenLifetime-gitAppTokenRefreshMargin {
		return path, nil
	}

	token, err := gitAppToken(ctx, auth.appID, auth.installationID, auth.appKeyFile)
	if err != nil {
		// A still-valid token keeps builds running through a GitHub outage.
		if age < installationTokenLifetime {
			logger.Warn("using cached github app token", "err", err)
			return path, nil
		}
		return "", err
	}

	return path, writeFileAtomic(path, []byte(token))
}

// writeFileAtomic replaces path with data, so a reader never sees a partial
// file.
func writeFileAtomic(path string, data []byte) error {
	// CreateTemp creates the file with mode 0600.
	f, err := os.CreateTemp(filepath.Dir(path), ".github-app-token-")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)

	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}

	return os.Rename(name, path)
}

// gitAppToken exchanges a GitHub App private key for an installation token.
//
// The token asks for contents:read only. That is all a kustomize build needs,
// and it keeps working if the App is later given more permissions for another
// consumer.
func gitAppToken(ctx context.Context, appID, installationID, privateKeyFile string) (string, error) {
	keyPEM, err := os.ReadFile(privateKeyFile)
	if err != nil {
		return "", err
	}

	key, err := parseGitAppKey(keyPEM)
	if err != nil {
		return "", err
	}

	assertion, err := gitAppJWT(appID, key, time.Now())
	if err != nil {
		return "", err
	}

	body, err := json.Marshal(struct {
		Permissions map[string]string `json:"permissions"`
	}{Permissions: map[string]string{"contents": "read"}})
	if err != nil {
		return "", err
	}

	apiURL := fmt.Sprintf("%s/app/installations/%s/access_tokens", gitHubAPIBaseURL, url.PathEscape(installationID))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+assertion)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return "", fmt.Errorf("unexpected status %d body %q", resp.StatusCode, msg)
	}

	var token struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&token); err != nil {
		return "", err
	}
	if token.Token == "" {
		return "", fmt.Errorf("response carried no token")
	}

	return token.Token, nil
}

// gitAppJWT builds the short-lived assertion GitHub accepts for App API calls.
func gitAppJWT(appID string, key *rsa.PrivateKey, now time.Time) (string, error) {
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, nil)
	if err != nil {
		return "", err
	}

	claims := jwt.Claims{
		Issuer: appID,
		// Backdate by a minute to absorb clock drift, and stay under GitHub's
		// ten minute ceiling.
		IssuedAt: jwt.NewNumericDate(now.Add(-time.Minute)),
		Expiry:   jwt.NewNumericDate(now.Add(9 * time.Minute)),
	}

	return jwt.Signed(signer).Claims(claims).Serialize()
}

// parseGitAppKey accepts the PKCS#1 key GitHub generates and the PKCS#8 form
// other tooling produces.
func parseGitAppKey(keyPEM []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(keyPEM)
	if block == nil {
		return nil, fmt.Errorf("no PEM block found")
	}

	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}

	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("unable to parse private key err:%w", err)
	}

	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("private key is %T, want RSA", parsed)
	}

	return key, nil
}
