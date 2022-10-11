package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func ensureBuild(ctx context.Context, cwd string, app applicationInfo) (string, error) {
	c, err := setupGitSSH(ctx, cwd, app)
	if err != nil {
		return "", err
	}

	//
	env := os.Environ()

	env = append(env, c)
	// Set HOME to cwd, this means that SSH should not pick up any
	// local SSH keys and use them for cloning
	env = append(env, fmt.Sprintf("HOME=%s", cwd))

	return runKustomizeBuild(ctx, cwd, env)
}

// runKustomizeBuild will run `kustomize build` cmd and return generated yaml or error
func runKustomizeBuild(ctx context.Context, cwd string, env []string) (string, error) {
	k := exec.CommandContext(ctx, "kustomize", "build", ".")

	k.Dir = cwd
	k.Env = env

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	k.Stdout = &stdout
	k.Stderr = &stderr

	if err := k.Start(); err != nil {
		return "", fmt.Errorf("unable to start kustomize cmd err:%s", err)
	}

	if err := k.Wait(); err != nil {
		return "", fmt.Errorf("error running kustomize err:%s", strings.TrimSpace(stderr.String()))
	}

	return stdout.String(), nil
}
