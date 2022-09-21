package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

func ensureBuild(ctx context.Context, clientset *kubernetes.Clientset, cwd string, app argoApp) (string, error) {
	// get keyring secrets
	keyringSecret, err := clientset.CoreV1().Secrets(app.namespace).Get(ctx, "strongbox-keyring", metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("error getting secret from %s namespace err:%s", app.namespace, err)
	}

	// create temp strongbox keyRing file
	keyRing, err := os.CreateTemp(cwd, ".strongbox-keyring-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(keyRing.Name())

	_, err = keyRing.Write(keyringSecret.Data[".strongbox_keyring"])
	if err != nil {
		return "", fmt.Errorf("unable to write to temp strongbox keyring err:%s", err)
	}

	keyRing.Close()

	if err := runStrongboxDecryption(ctx, cwd, keyRing.Name()); err != nil {
		return "", fmt.Errorf("unable to decrypt err:%s", err)
	}

	return runKustomizeBuild(ctx, cwd)
}

// runStrongboxDecryption will try to decrypt files in cwd using given keyRing file
func runStrongboxDecryption(ctx context.Context, cwd, keyringPath string) error {
	s := exec.CommandContext(ctx, "strongbox", "-keyring", keyringPath, "-decrypt", "-recursive", ".")
	s.Dir = cwd

	stderr, err := s.CombinedOutput()
	if err != nil {
		return fmt.Errorf("error running strongbox err:%s ", stderr)
	}

	return nil
}

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
