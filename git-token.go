package main

import (
	"fmt"
	"strings"
)

// gitTokenEnv returns the environment entries git needs to authenticate to
// GitHub with a token from a file.
//
// Kustomize strips the scheme and the user from ssh:// base URLs before it
// hands them to git (ssh://github.com/org/repo becomes github.com:org/repo),
// so every shape the URL can take is rewritten to https before git asks for
// credentials. Existing kustomization files stay as they are.
//
// Git reads the file only when it asks for credentials. That means public
// repos keep building when the file is missing or empty, and a rotated token
// is picked up on the next call.
//
// discardOnReject deletes the file when git reports the token was rejected, so
// a token the plugin minted is replaced on the next build. A mounted token file
// is left alone: the plugin cannot replace it.
func gitTokenEnv(tokenFile string, discardOnReject bool) []string {
	// Quote the path because the helper runs through sh.
	quoted := shellQuote(tokenFile)

	erase := ""
	if discardOnReject {
		// Git sends the rejected credential on stdin, so only delete the file
		// when it still holds that token: another build may have replaced it.
		erase = fmt.Sprintf(`
erase)
	t=%s
	while IFS= read -r l; do case "$l" in password=*) p="${l#password=}";; esac; done
	if [ "$p" = "$(cat "$t" 2>/dev/null)" ]; then rm -f "$t"; fi
;;`, quoted)
	}

	helper := fmt.Sprintf(`!f() { case "$1" in
get)
	echo username=x-access-token
	echo "password=$(cat %s)"
;;%s
esac; }; f`, quoted, erase)

	config := [][2]string{
		{"url.https://github.com/.insteadOf", "github.com:"},
		{"url.https://github.com/.insteadOf", "git@github.com:"},
		{"url.https://github.com/.insteadOf", "ssh://github.com/"},
		{"url.https://github.com/.insteadOf", "ssh://git@github.com/"},
		{"credential.https://github.com.helper", helper},
	}

	env := []string{fmt.Sprintf("GIT_CONFIG_COUNT=%d", len(config))}
	for i, c := range config {
		env = append(env,
			fmt.Sprintf("GIT_CONFIG_KEY_%d=%s", i, c[0]),
			fmt.Sprintf("GIT_CONFIG_VALUE_%d=%s", i, c[1]),
		)
	}

	// Fail instead of waiting for a credential prompt nobody can answer.
	env = append(env, "GIT_TERMINAL_PROMPT=0")

	return env
}

// shellQuote wraps s in single quotes so it survives being run by sh.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
