package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHasSSHRemoteBaseURL(t *testing.T) {
	type args struct {
		cwd string
	}
	tests := []struct {
		name    string
		args    args
		want    bool
		wantErr bool
	}{
		{"with remote base", args{"./testData/app-with-remote-base"}, true, false},
		{"without remote base", args{"./testData/app-with-secrets"}, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			files, err := findKustomizeFiles(tt.args.cwd)
			if err != nil {
				t.Fatal(err)
			}
			got, err := hasSSHRemoteBaseURL(files)
			if (err != nil) != tt.wantErr {
				t.Errorf("hasSSHRemoteBaseURL() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("hasSSHRemoteBaseURL() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCiphertextSecrets(t *testing.T) {
	t.Run("Error on STRONGBOX header in Secret data", func(t *testing.T) {
		yamlData := `
apiVersion: v1
kind: ServiceAccount
metadata:
  name: test
---
apiVersion: v1
kind: Secret
metadata:
  name: my-secret
data:
  key1: |
    c2VjcmV0ZGF0YQ==
  key2: |
    IyBTVFJPTkdCT1ggRU5DUllQVEVEIFJFU09VUkNFIDsgU2VlIGh0dHBzOi8vZ2l0aHViLmNvbS91
    dy1sYWJzL3N0cm9uZ2JveApiZlY0ZWZnVjNwTVVJUmRwV0VzbDFCdnJTNUo0QXZHcnd1eWNpZ0Y4
    eXZtUWVGUGNMNktFZGxRbjROOEtzVDhWNHJiUm45TVlIWXFUCmtoQ1d2bEMxWjh2QXJGcVhRdkhz
    UGF4M2lRPT0K
`
		err := checkSecrets([]byte(yamlData))
		if err == nil {
			t.Error("Expected error due to STRONGBOX header, but got nil")
		}
	})

	t.Run("Success with no STRONGBOX header in Secret data", func(t *testing.T) {
		yamlData := `
apiVersion: v1
kind: ConfigMap
metadata:
  name: test
data:
  key: value
  key1: value1
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: test
---
apiVersion: v1
kind: Secret
metadata:
  name: my-secret
data:
  key1: |
    c2VjcmV0ZGF0YQ==
`
		err := checkSecrets([]byte(yamlData))
		if err != nil {
			t.Errorf("Expected success, but got error: %v", err)
		}
	})

	t.Run("Error on AGE header in Secret data", func(t *testing.T) {
		yamlData := `
apiVersion: v1
kind: ServiceAccount
metadata:
  name: test
---
apiVersion: v1
kind: Secret
metadata:
  name: my-secret
data:
  key1: |
    c2VjcmV0ZGF0YQ==
  key2: |
    LS0tLS1CRUdJTiBBR0UgRU5DUllQVEVEIEZJTEUtLS0tLQpZV2RsTFdWdVkzSjVjSFJwYjI0dWIz
    Sm5MM1l4Q2kwK0lGZ3lOVFV4T1NCMVNHbHdXRkJMT0VwbVpHOWlUM1JTClMzQk5aREZPYm5Ndllr
    c3pkMVpwTUVsTldXSXpXRVEyWmtRMENqUnJPRTVuUVV3dlVrNWpZWFZTV1RaalNUVXoKUzBOdWRu
    RXpWWE5WVFhBeVpFcHZaMjl2V0ZwSVN6Z0tMUzB0SUROWVIweFpkM2ROVG5admF6QkRjM2RJWm1G
    SQpZMDQ1Ukc1WVlsWnJUMmREWVdZek1GRTVhVk5RYVRRS05DYmE3QzU1S01FWFp2MjU4bFU2WjFD
    M1c4UUF0WklGClJxZXFQSXZKYTljRTU0YUFDQT09Ci0tLS0tRU5EIEFHRSBFTkNSWVBURUQgRklM
    RS0tLS0tCg==
`
		err := checkSecrets([]byte(yamlData))
		if err == nil {
			t.Error("Expected error due to AGE header, but got nil")
		}
	})
}

func Test_ensureBuild_httpsBaseGetsToken(t *testing.T) {
	cwd := t.TempDir()
	kustomization := `resources:
  - https://github.com/utilitywarehouse/private-repo//manifests?ref=main
`
	if err := os.WriteFile(filepath.Join(cwd, "kustomization.yaml"), []byte(kustomization), 0o600); err != nil {
		t.Fatal(err)
	}

	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte("ghs_ABC123"), 0o600); err != nil {
		t.Fatal(err)
	}

	// The point of this test is the environment git inherits, not the build
	// itself, so stand in a kustomize that prints its environment.
	binDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(binDir, "kustomize"), []byte("#!/bin/sh\nenv\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	got, err := ensureBuild(context.Background(), cwd, gitAuth{tokenFile: tokenFile}, applicationInfo{})
	if err != nil {
		t.Fatal(err)
	}

	// An https base needs credentials even though it has no ssh:// prefix.
	for _, want := range []string{
		"GIT_CONFIG_COUNT=5",
		"GIT_CONFIG_KEY_0=url.https://github.com/.insteadOf",
		"GIT_CONFIG_VALUE_0=github.com:",
		"GIT_CONFIG_VALUE_4=!f() {",
		// No key was configured, so the build keeps the deny-all ssh command.
		"GIT_SSH_COMMAND=ssh -q -F none",
	} {
		if !strings.Contains(string(got), want) {
			t.Errorf("kustomize environment missing %q:\n%s", want, got)
		}
	}
}
