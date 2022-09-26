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

	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var (
	encryptedFilePrefix   = []byte("# STRONGBOX ENCRYPTED RESOURCE")
	errEncryptedFileFound = errors.New("encrypted file found")
)

func ensureDecryption(ctx context.Context, cwd, secretName, secretKey string) error {
	d, err := getKeyRingData(ctx, appNamespace, secretName, secretKey)
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

// getKeyRingData reads kube secret from given namespace and gets Keyring file data
func getKeyRingData(ctx context.Context, namespace, secretName, key string) ([]byte, error) {
	keyringSecret, err := clientset.CoreV1().Secrets(namespace).Get(ctx, secretName, metaV1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("unable to get secret secret:%s namespace:%s err:%s", secretName, namespace, err)
	}

	if v, ok := keyringSecret.Data[key]; ok {
		return v, nil
	}

	return nil, fmt.Errorf("key '%s' not found secret:'%s' namespace:%s", key, secretName, namespace)
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
	s := exec.CommandContext(ctx, "strongbox", "-keyring", keyringPath, "-decrypt", "-recursive", ".")
	s.Dir = cwd

	stderr, err := s.CombinedOutput()
	if err != nil {
		return fmt.Errorf("error running strongbox err:%s ", stderr)
	}

	return nil
}
