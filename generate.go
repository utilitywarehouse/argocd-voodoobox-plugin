package main

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// runKustomizeBuild will run `kustomize build` cmd and return generated yaml or error
func runKustomizeBuild(ctx context.Context, cwd string) (string, error) {
	k := exec.CommandContext(ctx, "kustomize", "build", ".")
	k.Dir = cwd

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
