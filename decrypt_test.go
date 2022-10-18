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
	// withRemoteBase        = "./testData/app-with-remote-base"
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
		fmt.Fprintf(os.Stderr, "%v", string(out))
		os.Exit(1)
	}

	cmd = exec.Command("cp", "-r", "./testData/app-with-secrets", encryptedTestDir2)
	out, err = cmd.CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v", string(out))
		os.Exit(1)
	}

	cmd = exec.Command("cp", "-r", "./testData/app-with-remote-base", withRemoteBaseTestDir)
	out, err = cmd.CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v", string(out))
		os.Exit(1)
	}

	code := m.Run()

	os.RemoveAll(encryptedTestDir1)
	os.RemoveAll(encryptedTestDir2)
	os.RemoveAll(withRemoteBaseTestDir)

	os.Exit(code)
}

func Test_hasEncryptedFiles(t *testing.T) {
	type args struct {
		cwd string
	}
	tests := []struct {
		name    string
		args    args
		want    bool
		wantErr bool
	}{
		{"encryptedTestDir1", args{cwd: encryptedTestDir1}, true, false},
		{"encryptedTestDir2", args{cwd: encryptedTestDir2}, true, false},
		{"withRemoteBase", args{cwd: withRemoteBaseTestDir}, false, false},
		{".github", args{cwd: ".github"}, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := hasEncryptedFiles(tt.args.cwd)
			if (err != nil) != tt.wantErr {
				t.Errorf("hasEncryptedFiles() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("hasEncryptedFiles() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_getKeyRingData(t *testing.T) {
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
	)

	type args struct {
		destinationNamespace string
		secret               secretInfo
	}
	tests := []struct {
		name    string
		args    args
		want    []byte
		wantErr bool
	}{
		{"bar", args{"bar", secretInfo{name: "argocd-strongbox-secret", key: ".strongbox_keyring"}}, []byte("keyring-data-bar"), false},
		{"foo-wrong-key", args{"foo", secretInfo{name: "strongbox-secret", key: ".strongbox_keyring"}}, nil, true},
		{"foo", args{"foo", secretInfo{name: "strongbox-secret", key: "randomKey"}}, []byte("keyring-data-foo"), false},
		{"missing", args{"default", secretInfo{name: "strongbox-secret", key: "randomKey"}}, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := getKeyRingData(context.Background(), tt.args.destinationNamespace, tt.args.secret)
			if (err != nil) != tt.wantErr {
				t.Errorf("getKeyRingData() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("getKeyRingData() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_ensureDecryption(t *testing.T) {
	secretAllowedNamespacesAnnotation = "argocd.voodoobox.plugin.io/allowed-namespaces"

	// read keyring file
	kr, err := os.ReadFile(encryptedTestDir1 + "/.keyRing")
	if err != nil {
		t.Fatal(err)
	}

	kubeClient = fake.NewSimpleClientset(
		&v1.Secret{
			ObjectMeta: metaV1.ObjectMeta{
				Name:      "strongbox-secret",
				Namespace: "foo",
			},
			Data: map[string][]byte{
				"keyring": kr,
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
				"keyring": kr,
			},
		},
	)

	// withRemoteBase doesn't have enc files so it should not look for "missing-secrets" secret
	bar := applicationInfo{
		name:                 "bar",
		destinationNamespace: "bar",
		keyringSecret: secretInfo{
			name: "missing-secrets",
			key:  "invalid",
		},
	}
	err = ensureDecryption(context.Background(), withRemoteBaseTestDir, bar)
	if err != nil {
		t.Fatal(err)
	}

	// encryptedTestDir1 has enc files so it should look for secret and then decrypt content
	// keyring secret in app's destination NS
	foo := applicationInfo{
		name:                 "foo",
		destinationNamespace: "foo",
		keyringSecret: secretInfo{
			name: "strongbox-secret",
			key:  "keyring",
		},
	}
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

	// encryptedTestDir2 has enc files so it should look for secret and then decrypt content
	// keyring secret in different namespace then app's destination NS
	baz := applicationInfo{
		name:                 "foo",
		destinationNamespace: "baz",
		keyringSecret: secretInfo{
			namespace: "not-baz",
			name:      "strongbox-secret",
			key:       "keyring",
		},
	}
	err = ensureDecryption(context.Background(), encryptedTestDir2, baz)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Contains(getFileContent(t, encryptedTestDir2+"/secrets/strongbox-keyring"), kr) {
		t.Error(encryptedTestDir2 + "/secrets/strongbox-keyring should contain keyring data")
	}

	encryptedFiles = []string{
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

}
