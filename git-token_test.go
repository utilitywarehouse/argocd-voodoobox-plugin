package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func Test_gitTokenEnv(t *testing.T) {
	// A path with a space makes sure the helper survives being run by sh.
	tokenDir := filepath.Join(t.TempDir(), "dir with space")
	if err := os.Mkdir(tokenDir, 0700); err != nil {
		t.Fatal(err)
	}
	tokenFile := filepath.Join(tokenDir, "token")

	wantEnv := []string{
		"GIT_CONFIG_COUNT=5",
		"GIT_CONFIG_KEY_0=url.https://github.com/.insteadOf",
		"GIT_CONFIG_VALUE_0=github.com:",
		"GIT_CONFIG_KEY_1=url.https://github.com/.insteadOf",
		"GIT_CONFIG_VALUE_1=git@github.com:",
		"GIT_CONFIG_KEY_2=url.https://github.com/.insteadOf",
		"GIT_CONFIG_VALUE_2=ssh://github.com/",
		"GIT_CONFIG_KEY_3=url.https://github.com/.insteadOf",
		"GIT_CONFIG_VALUE_3=ssh://git@github.com/",
		"GIT_CONFIG_KEY_4=credential.https://github.com.helper",
		`GIT_CONFIG_VALUE_4=!f() { test "$1" = get || return 0; echo username=x-access-token; echo "password=$(cat '` + tokenFile + `')"; }; f`,
		"GIT_TERMINAL_PROMPT=0",
	}

	if diff := cmp.Diff(wantEnv, gitTokenEnv(tokenFile)); diff != "" {
		t.Errorf("gitTokenEnv() mismatch (-want +got):\n%s", diff)
	}
}

// Test_gitTokenEnv_credentialHelper runs the helper through real git: it is
// shell code, and the token path is quoted, so only git can confirm it works.
func Test_gitTokenEnv_credentialHelper(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte("ghs_ABC123\n"), 0600); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("git", "credential", "fill")
	cmd.Env = append(os.Environ(), gitTokenEnv(tokenFile)...)
	cmd.Stdin = strings.NewReader("protocol=https\nhost=github.com\n\n")

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git credential fill error = %v, output:\n%s", err, out)
	}

	for _, want := range []string{"username=x-access-token", "password=ghs_ABC123"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("git credential fill output %q does not contain %q", out, want)
		}
	}
}

func Test_shellQuote(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: "/etc/github/token", want: `'/etc/github/token'`},
		{in: "/dir with space/token", want: `'/dir with space/token'`},
		{in: "/it's/token", want: `'/it'\''s/token'`},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := shellQuote(tt.in); got != tt.want {
				t.Errorf("shellQuote() = %s, want %s", got, tt.want)
			}
		})
	}
}
