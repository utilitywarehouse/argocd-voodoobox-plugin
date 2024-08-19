package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"filippo.io/age"
	"filippo.io/age/armor"
)

const (
	stronboxIdentityFilename = ".strongbox_identity"
)

var (
	encryptedFilePrefix   = []byte("# STRONGBOX ENCRYPTED RESOURCE")
	errEncryptedFileFound = errors.New("encrypted file found")
)

func ensureDecryption(ctx context.Context, cwd string, app applicationInfo) error {
	keyringData, identityData, err := secretData(ctx, app.destinationNamespace, app.keyringSecret)
	if err != nil {
		return err
	}
	if keyringData == nil && identityData == nil {
		return nil
	}

	// create strongbox keyRing file
	if keyringData != nil {
		keyRingPath := filepath.Join(cwd, strongboxKeyRingFile)
		if err := os.WriteFile(keyRingPath, keyringData, 0644); err != nil {
			return err
		}

		if err := runStrongboxDecryption(ctx, cwd, keyRingPath); err != nil {
			return fmt.Errorf("unable to decrypt err:%s", err)
		}
	}

	if identityData != nil {
		identityPath := filepath.Join(cwd, stronboxIdentityFilename)
		if err := os.WriteFile(identityPath, identityData, 0644); err != nil {
			return err
		}
		if err := strongboxAgeRecursiveDecrypt(ctx, cwd, identityData); err != nil {
			return fmt.Errorf("unable to decrypt err:%s", err)
		}
	}

	return nil
}

func secretData(ctx context.Context, destinationNamespace string, si secretInfo) ([]byte, []byte, error) {
	secret, err := getSecret(ctx, destinationNamespace, si)
	if err != nil {
		return nil, nil, err
	}

	return secret.Data[si.key], secret.Data[stronboxIdentityFilename], nil
}

// runStrongboxDecryption will try to decrypt files in cwd using given keyRing file
func runStrongboxDecryption(ctx context.Context, cwd, keyringPath string) error {
	s := exec.CommandContext(ctx, "strongbox", "-keyring", keyringPath, "-decrypt", "-recursive", cwd)

	stderr, err := s.CombinedOutput()
	if err != nil {
		return fmt.Errorf("error running strongbox err:%s ", stderr)
	}

	return nil
}

func strongboxAgeRecursiveDecrypt(ctx context.Context, cwd string, identityData []byte) error {
	identities, err := age.ParseIdentities(bytes.NewBuffer(identityData))
	if err != nil {
		return err
	}

	return filepath.Walk(cwd, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			// skip .git directory
			if info.Name() == ".git" {
				return fs.SkipDir
			}
			return nil
		}

		file, err := os.OpenFile(path, os.O_RDWR, 0644)
		if err != nil {
			return err
		}
		defer file.Close()
		in, err := io.ReadAll(file)
		if err != nil {
			return err
		}

		if !strings.HasPrefix(string(in), armor.Header) {
			return nil
		}

		armorReader := armor.NewReader(bytes.NewReader(in))
		ar, err := age.Decrypt(armorReader, identities...)
		if err != nil {
			return err
		}

		if err = file.Truncate(0); err != nil {
			return err
		}
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return err
		}
		_, err = io.Copy(file, ar)
		if err != nil {
			return err
		}

		return nil
	})
}
