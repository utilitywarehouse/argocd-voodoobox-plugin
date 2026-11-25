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
	const tokenFile = "/dir with space/token"

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
		`GIT_CONFIG_VALUE_4=!f() { case "$1" in
get)
	echo username=x-access-token
	echo "password=$(cat '/dir with space/token')"
;;
erase)
	t='/dir with space/token'
	while IFS= read -r l; do case "$l" in password=*) p="${l#password=}";; esac; done
	if [ "$p" = "$(cat "$t" 2>/dev/null)" ]; then rm -f "$t"; fi
;;
esac; }; f`,
		"GIT_TERMINAL_PROMPT=0",
	}

	if diff := cmp.Diff(wantEnv, gitTokenEnv(tokenFile, true)); diff != "" {
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
	cmd.Env = append(os.Environ(), gitTokenEnv(tokenFile, false)...)
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

// Test_gitTokenEnv_discardsRejectedToken runs git's reject path: when GitHub
// rejects the credentials git calls the helper with "erase", and a token the
// plugin minted must be dropped so the next build mints a new one.
func Test_gitTokenEnv_discardsRejectedToken(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	tests := []struct {
		name             string
		discardOnReject  bool
		rejectedPassword string
		wantRemoved      bool
	}{
		{name: "rejected token is discarded", discardOnReject: true, rejectedPassword: "ghs_ABC123", wantRemoved: true},
		{name: "a newer token is kept", discardOnReject: true, rejectedPassword: "ghs_OLD", wantRemoved: false},
		{name: "mounted token file is kept", discardOnReject: false, rejectedPassword: "ghs_ABC123", wantRemoved: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tokenFile := filepath.Join(t.TempDir(), "token")
			if err := os.WriteFile(tokenFile, []byte("ghs_ABC123\n"), 0600); err != nil {
				t.Fatal(err)
			}

			cmd := exec.Command("git", "credential", "reject")
			cmd.Env = append(os.Environ(), gitTokenEnv(tokenFile, tt.discardOnReject)...)
			cmd.Stdin = strings.NewReader(
				"protocol=https\nhost=github.com\nusername=x-access-token\npassword=" + tt.rejectedPassword + "\n\n",
			)

			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git credential reject error = %v, output:\n%s", err, out)
			}

			_, err := os.Stat(tokenFile)
			if removed := os.IsNotExist(err); removed != tt.wantRemoved {
				t.Errorf("token file removed = %v, want %v", removed, tt.wantRemoved)
			}
		})
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
