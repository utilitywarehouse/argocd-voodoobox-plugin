package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"testing"

	v1 "k8s.io/api/core/v1"
	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

var (
	encryptedTestDir1     = "./testData/app-with-secrets-test1"
	encryptedTestDir2     = "./testData/app-with-secrets-test2"
	withRemoteBaseTestDir = "./testData/app-with-remote-base-test1"
)

func getFileContent(t *testing.T, fileName string) []byte {
	data, err := os.ReadFile(fileName)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestMain(m *testing.M) {
	// create copy of encrypted test dir as this tests will modify files
	cmd := exec.Command("cp", "-r", "./testData/app-with-secrets", encryptedTestDir1)
	out, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s", out)
		os.Exit(1)
	}

	cmd = exec.Command("cp", "-r", "./testData/app-with-secrets", encryptedTestDir2)
	out, err = cmd.CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s", out)
		os.Exit(1)
	}

	cmd = exec.Command("cp", "-r", "./testData/app-with-remote-base", withRemoteBaseTestDir)
	out, err = cmd.CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s", out)
		os.Exit(1)
	}

	code := m.Run()

	os.RemoveAll(encryptedTestDir1)
	os.RemoveAll(encryptedTestDir2)
	os.RemoveAll(withRemoteBaseTestDir)

	os.Exit(code)
}

func Test_secretData(t *testing.T) {
	kubeClient = fake.NewSimpleClientset(
		&v1.Secret{
			ObjectMeta: metaV1.ObjectMeta{
				Name:      "argocd-strongbox-secret",
				Namespace: "bar",
			},
			Data: map[string][]byte{
				".strongbox_keyring": []byte("keyring-data-bar"),
			},
		},
		&v1.Secret{
			ObjectMeta: metaV1.ObjectMeta{
				Name:      "strongbox-secret",
				Namespace: "foo",
			},
			Data: map[string][]byte{
				"randomKey": []byte("keyring-data-foo"),
			},
		},
		&v1.Secret{
			ObjectMeta: metaV1.ObjectMeta{
				Name:      "argocd-voodoobox-strongbox-keyring",
				Namespace: "age",
			},
			Data: map[string][]byte{
				strongboxIdentityFilename: []byte("AGE-SECRET-KEY-1GNC98E3WNPAXE49FATT434CFC2THV5Q0SLW45T3VNYUVZ4F8TY6SREQR9Q"),
			},
		},
		&v1.Secret{
			ObjectMeta: metaV1.ObjectMeta{
				Name:      "argocd-voodoobox-strongbox-keyring",
				Namespace: "age-and-siv",
			},
			Data: map[string][]byte{
				".strongbox_keyring":      []byte("keyring-data-bar"),
				strongboxIdentityFilename: []byte("AGE-SECRET-KEY-1GNC98E3WNPAXE49FATT434CFC2THV5Q0SLW45T3VNYUVZ4F8TY6SREQR9Q"),
			},
		},
	)

	tests := []struct {
		name                 string
		destinationNamespace string
		secret               secretInfo
		keyring              []byte
		identity             []byte
		wantErr              bool
	}{
		{"bar-siv-ok", "bar", secretInfo{name: "argocd-strongbox-secret"}, []byte("keyring-data-bar"), nil, false},
		{"age-ok", "age", secretInfo{name: "argocd-voodoobox-strongbox-keyring"}, nil, []byte("AGE-SECRET-KEY-1GNC98E3WNPAXE49FATT434CFC2THV5Q0SLW45T3VNYUVZ4F8TY6SREQR9Q"), false},
		{"age-and-siv-ok", "age-and-siv", secretInfo{name: "argocd-voodoobox-strongbox-keyring"}, []byte("keyring-data-bar"), []byte("AGE-SECRET-KEY-1GNC98E3WNPAXE49FATT434CFC2THV5Q0SLW45T3VNYUVZ4F8TY6SREQR9Q"), false},
		{"foo-wrong-key", "foo", secretInfo{name: "strongbox-secret"}, nil, nil, false},
		{"default-missing", "default", secretInfo{name: "strongbox-secret"}, nil, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			keyringData, identityData, err := secretData(context.Background(), tt.destinationNamespace, tt.secret)
			if (err != nil) != tt.wantErr {
				t.Errorf("secretData() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(keyringData, tt.keyring) {
				t.Errorf("secretData() keyring=%s, want=%s", keyringData, tt.keyring)
			}
			if !reflect.DeepEqual(identityData, tt.identity) {
				t.Errorf("secretData() identity=%s, want=%s", identityData, tt.identity)
			}
		})
	}
}

func Test_ensureDecryption(t *testing.T) {
	allowedNamespacesSecretAnnotation = "argocd.voodoobox.plugin.io/allowed-namespaces"

	// read keyring file
	kr, err := os.ReadFile(encryptedTestDir1 + "/.keyRing")
	if err != nil {
		t.Fatal(err)
	}

	kubeClient = fake.NewSimpleClientset(
		&v1.Secret{
			ObjectMeta: metaV1.ObjectMeta{
				Name:      "strongbox-secret",
				Namespace: "bar",
			},
			Data: map[string][]byte{
				".strongbox_keyring": kr,
			},
		},
		&v1.Secret{
			ObjectMeta: metaV1.ObjectMeta{
				Name:      "strongbox-secret",
				Namespace: "foo",
			},
			Data: map[string][]byte{
				".strongbox_keyring": kr,
			},
		},
		&v1.Secret{
			ObjectMeta: metaV1.ObjectMeta{
				Name:      "strongbox-secret",
				Namespace: "not-baz",
				Annotations: map[string]string{
					"argocd.voodoobox.plugin.io/allowed-namespaces": "baz,rand",
				},
			},
			Data: map[string][]byte{
				".strongbox_keyring": kr,
			},
		},
	)

	// withRemoteBase doesn't have encrypted files but namespace contains secret so it should setup
	// strongbox for remote base's encrypted secrets
	bar2 := applicationInfo{
		name:                 "bar",
		destinationNamespace: "bar",
		keyringSecret: secretInfo{
			name: "strongbox-secret",
		},
	}
	t.Run("no-encrypted-files-with-secret", func(t *testing.T) {
		err = ensureDecryption(context.Background(), withRemoteBaseTestDir, bar2)
		if err != nil {
			t.Fatal(err)
		}
		// make sure .strongbox_keyring file exists with correct keyring data
		if !bytes.Contains(getFileContent(t, withRemoteBaseTestDir+"/.strongbox_keyring"), kr) {
			t.Error(withRemoteBaseTestDir + "/.strongbox_keyring should contain keyring data")
		}
	})

	// encryptedTestDir1 has encrypted files so it should look for secret and then decrypt content
	// keyring secret in app's destination NS
	foo := applicationInfo{
		name:                 "foo",
		destinationNamespace: "foo",
		keyringSecret: secretInfo{
			name: "strongbox-secret",
		},
	}
	t.Run("encrypted-files-with-secret", func(t *testing.T) {
		err = ensureDecryption(context.Background(), encryptedTestDir1, foo)
		if err != nil {
			t.Fatal(err)
		}

		if !bytes.Contains(getFileContent(t, encryptedTestDir1+"/secrets/strongbox-keyring"), kr) {
			t.Error(encryptedTestDir1 + "/secrets/strongbox-keyring should contain keyring data")
		}

		encryptedFiles := []string{
			encryptedTestDir1 + "/app/secrets/env_secrets",
			encryptedTestDir1 + "/app/secrets/kube_secret.yaml",
			encryptedTestDir1 + "/app/secrets/s1.json",
			encryptedTestDir1 + "/app/secrets/s2.yaml",
		}

		for _, f := range encryptedFiles {
			if !bytes.Contains(getFileContent(t, f), []byte("PlainText")) {
				t.Errorf("%s should be decrypted", f)
			}
		}
	})

	// encryptedTestDir2 has encrypted files so it should look for secret and then decrypt content
	// keyring secret in different namespace then app's destination NS
	baz := applicationInfo{
		name:                 "foo",
		destinationNamespace: "baz",
		keyringSecret: secretInfo{
			namespace: "not-baz",
			name:      "strongbox-secret",
		},
	}
	t.Run("encrypted-files-with-secret-from-diff-ns", func(t *testing.T) {
		err = ensureDecryption(context.Background(), encryptedTestDir2, baz)
		if err != nil {
			t.Fatal(err)
		}

		if !bytes.Contains(getFileContent(t, encryptedTestDir2+"/secrets/strongbox-keyring"), kr) {
			t.Error(encryptedTestDir2 + "/secrets/strongbox-keyring should contain keyring data")
		}

		encryptedFiles := []string{
			encryptedTestDir2 + "/app/secrets/env_secrets",
			encryptedTestDir2 + "/app/secrets/kube_secret.yaml",
			encryptedTestDir2 + "/app/secrets/s1.json",
			encryptedTestDir2 + "/app/secrets/s2.yaml",
		}

		for _, f := range encryptedFiles {
			if !bytes.Contains(getFileContent(t, f), []byte("PlainText")) {
				t.Errorf("%s should be decrypted", f)
			}
		}
	})

}
