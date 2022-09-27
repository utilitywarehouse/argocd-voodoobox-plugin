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
	encryptedTestDir = "./testData/app-with-secrets-test"
	plainTextTestDir = "./testData/app-without-secrets"
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
	cmd := exec.Command("cp", "-r", "./testData/app-with-secrets", encryptedTestDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v", string(out))
		os.Exit(1)
	}

	code := m.Run()

	os.RemoveAll(encryptedTestDir)

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
		{"encryptedTestDir", args{cwd: encryptedTestDir}, true, false},
		{"plainTextTestDir", args{cwd: plainTextTestDir}, false, false},
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
	kubeClient := fake.NewSimpleClientset(
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
		namespace  string
		secretName string
		key        string
	}
	tests := []struct {
		name    string
		args    args
		want    []byte
		wantErr bool
	}{
		{"bar", args{"bar", "argocd-strongbox-secret", ".strongbox_keyring"}, []byte("keyring-data-bar"), false},
		{"foo-wrong-key", args{"foo", "strongbox-secret", ".strongbox_keyring"}, nil, true},
		{"foo", args{"foo", "strongbox-secret", "randomKey"}, []byte("keyring-data-foo"), false},
		{"missing", args{"default", "strongbox-secret", "randomKey"}, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := getKeyRingData(context.Background(), kubeClient, tt.args.namespace, tt.args.secretName, tt.args.key)
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
	appNamespace = "foo"

	// read keyring file
	kr, err := os.ReadFile(encryptedTestDir + "/.keyRing")
	if err != nil {
		t.Fatal(err)
	}

	kubeClient := fake.NewSimpleClientset(
		&v1.Secret{
			ObjectMeta: metaV1.ObjectMeta{
				Name:      "strongbox-secret",
				Namespace: "foo",
			},
			Data: map[string][]byte{
				"keyring": kr,
			},
		},
	)

	// without-secrets doesn't have enc files so it should not look for "missing-secrets" secret
	err = ensureDecryption(context.Background(), kubeClient, plainTextTestDir, "missing-secrets", "invalid")
	if err != nil {
		t.Fatal(err)
	}

	// app-with-secrets has enc files so it should look for secret and then decrypt content
	err = ensureDecryption(context.Background(), kubeClient, encryptedTestDir, "strongbox-secret", "keyring")
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Contains(getFileContent(t, encryptedTestDir+"/secrets/strongbox-keyring"), kr) {
		t.Error(encryptedTestDir + "/secrets/strongbox-keyring should contain keyring data")
	}

	encryptedFiles := []string{
		encryptedTestDir + "/app/secrets/env_secrets",
		encryptedTestDir + "/app/secrets/kube_secret.yaml",
		encryptedTestDir + "/app/secrets/s1.json",
		encryptedTestDir + "/app/secrets/s2.yaml",
	}

	for _, f := range encryptedFiles {
		if !bytes.Contains(getFileContent(t, f), []byte("PlainText")) {
			t.Errorf("%s should be decrypted", f)
		}
	}

}
