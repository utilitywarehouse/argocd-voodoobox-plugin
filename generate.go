package main

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"filippo.io/age/armor"
	"github.com/ghodss/yaml"
	v1 "k8s.io/api/core/v1"
)

func ensureBuild(ctx context.Context, cwd, globalKeyPath, globalKnownHostFile string, app applicationInfo) ([]byte, error) {
	// Even when there is no git SSH secret defined, we still override the
	// Git SSH command (pointing the key to /dev/null) in order to avoid
	// using SSH keys in default system locations and to surface the error
	// if bases over SSH have been configured.
	sshCmdEnv := `GIT_SSH_COMMAND=ssh -q -F none -o IdentitiesOnly=yes -o IdentityFile=/dev/null -o UserKnownHostsFile=/dev/null -o StrictHostKeyChecking=no`

	kFiles, err := findKustomizeFiles(cwd)
	if err != nil {
		return nil, fmt.Errorf("unable to get Kustomize files paths err:%s", err)
	}

	if len(kFiles) == 0 {
		return findAndReadYamlFiles(cwd)
	}

	hasRemoteBase, err := hasSSHRemoteBaseURL(kFiles)
	if err != nil {
		return nil, fmt.Errorf("unable to look for SSH protocol err:%s", err)
	}

	if hasRemoteBase {
		sshCmdEnv, err = setupGitSSH(ctx, cwd, globalKeyPath, globalKnownHostFile, app)
		if err != nil {
			return nil, err
		}
	}

	// setup env for Kustomize command
	env := []string{
		// Set HOME to cwd, this means that SSH should not pick up any
		// local SSH keys and use them for cloning
		// HOME is also used to setup git config in current dir
		fmt.Sprintf("HOME=%s", cwd),
		fmt.Sprintf("PATH=%s", os.Getenv("PATH")),
	}

	env = append(env, sshCmdEnv)

	// setup Git config if .strongbox_keyring or .strongbox_identity exits
	if fileExists(filepath.Join(cwd, strongboxKeyringFilename)) || fileExists(filepath.Join(cwd, strongboxIdentityFilename)) {
		// setup SB home for kustomize run
		env = append(env, fmt.Sprintf("STRONGBOX_HOME=%s", cwd))

		// setup git config via `strongbox -git-config`
		if err := setupGitConfigForSB(ctx, cwd, env); err != nil {
			return nil, fmt.Errorf("unable setup git config for strongbox err:%s", err)
		}
	}

	return runKustomizeBuild(ctx, cwd, env)
}

func fileExists(filepath string) bool {
	_, err := os.Stat(filepath)
	return err == nil
}

func findKustomizeFiles(cwd string) ([]string, error) {
	kFiles := []string{}

	err := filepath.WalkDir(cwd, func(path string, info fs.DirEntry, err error) error {
		if filepath.Base(path) == "kustomization.yaml" ||
			filepath.Base(path) == "kustomization.yml" ||
			filepath.Base(path) == "Kustomization" {
			kFiles = append(kFiles, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return kFiles, nil
}

func hasSSHRemoteBaseURL(kFiles []string) (bool, error) {
	for _, k := range kFiles {
		data, err := os.ReadFile(k)
		if err != nil {
			return false, err
		}
		if bytes.Contains(data, []byte("ssh://")) {
			return true, nil
		}
	}
	return false, nil
}

// setupGitConfigForSB will setup git filters to run Strongbox
func setupGitConfigForSB(ctx context.Context, cwd string, env []string) error {
	s := exec.CommandContext(ctx, "strongbox", "-git-config")
	s.Dir = cwd
	s.Env = env

	stderr, err := s.CombinedOutput()
	if err != nil {
		return fmt.Errorf("error running strongbox err:%s ", stderr)
	}

	return nil
}

// runKustomizeBuild runs `kustomize build` and returns the generated YAML or an error.
func runKustomizeBuild(ctx context.Context, cwd string, env []string) ([]byte, error) {
	k := exec.CommandContext(ctx, "kustomize", "build", ".")
	k.Dir = cwd
	k.Env = env

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	k.Stdout = &stdout
	k.Stderr = &stderr
	start := time.Now()
	if err := k.Start(); err != nil {
		return nil, fmt.Errorf("unable to start kustomize cmd:  duration=%s err=%s", time.Since(start), err)
	}

	if err := k.Wait(); err != nil {
		return nil, fmt.Errorf("kustomize command error: duration=%s err=%s", time.Since(start), stderr.String())
	}

	logger.Info("kustomize command finished", "duration", time.Since(start))

	if err := checkSecrets(stdout.Bytes()); err != nil {
		return nil, err
	}

	return stdout.Bytes(), nil
}

func findAndReadYamlFiles(cwd string) ([]byte, error) {
	var content []byte
	err := filepath.WalkDir(cwd, func(path string, info fs.DirEntry, err error) error {
		if filepath.Ext(path) == ".yaml" || filepath.Base(path) == ".yml" {
			data, err := os.ReadFile(path)
			if err != nil {
				return fmt.Errorf("unable to read file %s err:%s", path, err)
			}
			content = append(content, []byte(fmt.Sprintf("%s\n---\n", data))...)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return content, nil
}

func checkSecrets(yamlData []byte) error {
	// Split input YAML into multiple documents by "---"
	docs := bytes.Split(yamlData, []byte("\n---\n"))

	for _, doc := range docs {
		if len(bytes.TrimSpace(doc)) == 0 {
			continue // Skip empty documents
		}

		var secret v1.Secret
		if err := yaml.Unmarshal(doc, &secret); err != nil {
			// Unmarshaling will fail for Secret like object, like ConfigMap
			continue
		}

		// Check if the decoded document is a Secret
		if secret.Kind == "Secret" {
			for key, val := range secret.Data {
				if bytes.HasPrefix(val, encryptedFilePrefix) || strings.HasPrefix(string(val), armor.Header) {
					return fmt.Errorf("found ciphertext in Secret: secret=%s key=%s", secret.Name, key)
				}
			}
		}
	}
	return nil
}
