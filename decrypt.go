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
)

var (
	encryptedFilePrefix   = []byte("# STRONGBOX ENCRYPTED RESOURCE")
	errEncryptedFileFound = errors.New("encrypted file found")
)

func ensureDecryption(ctx context.Context, cwd string, app applicationInfo) error {
	// Check if decryption is required before requesting keyRing secrets
	found, err := hasEncryptedFiles(cwd)
	if err != nil {
		return fmt.Errorf("unable to check if app source folder has encrypted files err:%s", err)
	}

	if !found {
		return nil
	}

	d, err := getKeyRingData(ctx, app.destinationNamespace, app.keyringSecret)
	if err != nil {
		return err
	}

	// create temp strongbox keyRing file
	keyRing, err := os.CreateTemp(cwd, ".strongbox-keyring-*")
	if err != nil {
		return fmt.Errorf("unable to create to temp file for strongbox keyring err:%s", err)
	}
	defer os.Remove(keyRing.Name())

	_, err = keyRing.Write(d)
	if err != nil {
		return fmt.Errorf("unable to write to temp strongbox keyring err:%s", err)
	}
	keyRing.Close()

	if err := runStrongboxDecryption(ctx, cwd, keyRing.Name()); err != nil {
		return fmt.Errorf("unable to decrypt err:%s", err)
	}
	return nil
}

func getKeyRingData(ctx context.Context, destinationNamespace string, secret secretInfo) ([]byte, error) {
	keyringSecret, err := getSecret(ctx, destinationNamespace, secret)
	if err != nil {
		return nil, err
	}

	if v, ok := keyringSecret.Data[secret.key]; ok {
		return v, nil
	}

	return nil, fmt.Errorf("key '%s' not found %s secret", secret.key, secret.name)
}

// hasEncryptedFiles will recursively check if any encrypted file
// it will return on first encrypted file if found
func hasEncryptedFiles(cwd string) (bool, error) {
	err := filepath.WalkDir(cwd, func(path string, entry fs.DirEntry, err error) error {
		// always return on error
		if err != nil {
			return err
		}

		if entry.IsDir() {
			// skip .git directory
			if entry.Name() == ".git" {
				return fs.SkipDir
			}
			return nil
		}

		file, err := os.OpenFile(path, os.O_RDONLY, 0)
		if err != nil {
			return err
		}
		defer file.Close()

		// for optimisation only read required chunk of the file and verify if encrypted
		chunk := make([]byte, 100)
		_, err = file.Read(chunk)
		if err != nil && err != io.EOF {
			return err
		}

		if bytes.Contains(chunk, encryptedFilePrefix) {
			return errEncryptedFileFound
		}

		return nil
	})

	if err != nil {
		if err == errEncryptedFileFound {
			return true, nil
		}
		return false, err
	}

	return false, nil
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
