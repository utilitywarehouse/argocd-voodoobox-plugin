package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
	v1 "k8s.io/api/core/v1"
	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func Test_updateRepoBaseAddresses(t *testing.T) {
	type args struct {
		in []byte
	}
	tests := []struct {
		name       string
		args       args
		wantOut    []byte
		wantKeyMap map[string]string
		wantErr    bool
	}{
		{
			name: "valid",
			args: args{
				in: []byte(`apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
  - app/

  - github.com/org/open1//manifests/lab-foo?ref=master
  # argocd-strongbox-plugin: key_a
  - ssh://github.com/org/repo1//manifests/lab-foo?ref=master
  # argocd-strongbox-plugin:keyD
  - ssh://github.com/org/repo3//manifests/lab-zoo?ref=dev
  # argocd-strongbox-plugin: sshKeyB
  - ssh://gitlab.io/org/repo2//manifests/lab-bar?ref=main
  # argocd-strongbox-plugin:  key_c
  - ssh://bitbucket.org/org/repo3//manifests/lab-zoo?ref=dev
`)},
			wantOut: []byte(`apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
  - app/

  - github.com/org/open1//manifests/lab-foo?ref=master
  # argocd-strongbox-plugin: key_a
  - ssh://key_a_github_com/org/repo1//manifests/lab-foo?ref=master
  # argocd-strongbox-plugin:keyD
  - ssh://keyD_github_com/org/repo3//manifests/lab-zoo?ref=dev
  # argocd-strongbox-plugin: sshKeyB
  - ssh://sshKeyB_gitlab_io/org/repo2//manifests/lab-bar?ref=main
  # argocd-strongbox-plugin:  key_c
  - ssh://key_c_bitbucket_org/org/repo3//manifests/lab-zoo?ref=dev
`),
			wantKeyMap: map[string]string{
				"key_a":   "github.com",
				"sshKeyB": "gitlab.io",
				"key_c":   "bitbucket.org",
				"keyD":    "github.com",
			},
		}, {
			name: "valid-with-empty-line",
			args: args{
				in: []byte(`apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
  - app/

  - github.com/org/open1//manifests/lab-foo?ref=master

  # argocd-strongbox-plugin: key_a
  - ssh://github.com/org/repo1//manifests/lab-foo?ref=master

  # argocd-strongbox-plugin:keyD
  - ssh://github.com/org/repo3//manifests/lab-zoo?ref=dev
  # argocd-strongbox-plugin: sshKeyB
  - ssh://gitlab.io/org/repo2//manifests/lab-bar?ref=main
  # argocd-strongbox-plugin:  key_c
  - ssh://bitbucket.org/org/repo3//manifests/lab-zoo?ref=dev
`)},
			wantOut: []byte(`apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
  - app/

  - github.com/org/open1//manifests/lab-foo?ref=master

  # argocd-strongbox-plugin: key_a
  - ssh://key_a_github_com/org/repo1//manifests/lab-foo?ref=master

  # argocd-strongbox-plugin:keyD
  - ssh://keyD_github_com/org/repo3//manifests/lab-zoo?ref=dev
  # argocd-strongbox-plugin: sshKeyB
  - ssh://sshKeyB_gitlab_io/org/repo2//manifests/lab-bar?ref=main
  # argocd-strongbox-plugin:  key_c
  - ssh://key_c_bitbucket_org/org/repo3//manifests/lab-zoo?ref=dev
`),
			wantKeyMap: map[string]string{
				"key_a":   "github.com",
				"sshKeyB": "gitlab.io",
				"key_c":   "bitbucket.org",
				"keyD":    "github.com",
			},
		},
		{
			name: "missing key ref",
			args: args{
				in: []byte(`apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
  - app/

  - github.com/org/open1//manifests/lab-foo?ref=master
  - ssh://github.com/org/repo3//manifests/lab-zoo?ref=dev
`)},
			wantOut:    nil,
			wantKeyMap: nil,
			wantErr:    true,
		},
		{
			name: "missing ssh protocol",
			args: args{
				in: []byte(`apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
  - app/

  - github.com/org/open1//manifests/lab-foo?ref=master
  # argocd-strongbox-plugin: key_c
  - github.com/org/repo3//manifests/lab-zoo?ref=dev
`)},
			wantOut:    nil,
			wantKeyMap: nil,
			wantErr:    true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotKeyMap, gotOut, err := updateRepoBaseAddresses(bytes.NewReader(tt.args.in))
			if (err != nil) != tt.wantErr {
				t.Errorf("updateRepoBaseAddresses() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if diff := cmp.Diff(tt.wantKeyMap, gotKeyMap); diff != "" {
				t.Errorf("updateRepoBaseAddresses() keyMap mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantOut, gotOut); diff != "" {
				t.Errorf("updateRepoBaseAddresses() output mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func Test_constructSSHConfig(t *testing.T) {
	type args struct {
		keyFilePaths map[string]string
		keyedDomain  map[string]string
	}
	tests := []struct {
		name    string
		args    args
		want    []string
		wantErr bool
	}{
		{"empty", args{nil, nil}, nil, true},
		{"single",
			args{
				keyFilePaths: map[string]string{
					"key_a": "path/to/this/key/key_a",
				},
				keyedDomain: nil,
			},
			[]string{`Host *
    IdentitiesOnly yes
    IdentityFile path/to/this/key/key_a
    User git
`},
			false,
		},
		{"multiple-keys",
			args{
				keyFilePaths: map[string]string{
					"key_a":   "path/to/this/key/key_a",
					"sshKeyB": "path/to/this/key/sshKeyB",
					"key_c":   "path/to/this/key/key_c",
					"keyD":    "path/to/this/key/keyD",
				},
				keyedDomain: map[string]string{
					"key_a":   "github.com",
					"sshKeyB": "gitlab.io",
					"key_c":   "bitbucket.org",
					"keyD":    "github.com",
				},
			},
			[]string{`Host key_a_github_com
    HostName github.com
    IdentitiesOnly yes
    IdentityFile path/to/this/key/key_a
    User git`,
				`Host sshKeyB_gitlab_io
    HostName gitlab.io
    IdentitiesOnly yes
    IdentityFile path/to/this/key/sshKeyB
    User git`,
				`Host key_c_bitbucket_org
    HostName bitbucket.org
    IdentitiesOnly yes
    IdentityFile path/to/this/key/key_c
    User git`,
				`Host keyD_github_com
    HostName github.com
    IdentitiesOnly yes
    IdentityFile path/to/this/key/keyD
    User git`},
			false,
		},
		// should this be allowed?
		// one valid case will be secret is referenced from diff namespace and only
		// few keys are used in current namespace
		{"key-from-secret-not-referenced",
			args{
				keyFilePaths: map[string]string{
					"key_a":   "path/to/this/key/key_a",
					"sshKeyB": "path/to/this/key/sshKeyB",
					"key_c":   "path/to/this/key/key_c",
					"keyD":    "path/to/this/key/keyD",
				},
				keyedDomain: map[string]string{
					"key_a":   "github.com",
					"sshKeyB": "gitlab.io",
					"key_c":   "bitbucket.org",
				},
			},
			[]string{`Host key_a_github_com
    HostName github.com
    IdentitiesOnly yes
    IdentityFile path/to/this/key/key_a
    User git`,
				`Host sshKeyB_gitlab_io
    HostName gitlab.io
    IdentitiesOnly yes
    IdentityFile path/to/this/key/sshKeyB
    User git`,
				`Host key_c_bitbucket_org
    HostName bitbucket.org
    IdentitiesOnly yes
    IdentityFile path/to/this/key/key_c
    User git`},
			false,
		},
		{"missing-referenced-key-from-secret",
			args{
				keyFilePaths: map[string]string{
					"key_a":   "path/to/this/key/key_a",
					"sshKeyB": "path/to/this/key/sshKeyB",
					"key_c":   "path/to/this/key/key_c",
				},
				keyedDomain: map[string]string{
					"key_a":   "github.com",
					"sshKeyB": "gitlab.io",
					"key_c":   "bitbucket.org",
					"keyD":    "github.com",
				},
			},
			nil,
			true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := constructSSHConfig(tt.args.keyFilePaths, tt.args.keyedDomain)
			if (err != nil) != tt.wantErr {
				t.Errorf("constructSSHConfig() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.want == nil && got != nil {
				t.Errorf("constructSSHConfig() got unexpected output got = %s", got)
				return
			}
			// since map is used to construct config it will be different all the time
			for _, w := range tt.want {
				if !bytes.Contains(got, []byte(w)) {
					t.Errorf("constructSSHConfig() %s\n\n ** missing from config **\n\n %s\n", w, got)
				}
			}

		})
	}
}

func Test_setupGitSSH(t *testing.T) {
	kubeClient = fake.NewSimpleClientset(
		&v1.Secret{
			ObjectMeta: metaV1.ObjectMeta{
				Name:      "argocd-git-ssh",
				Namespace: "foo",
			},
			Data: map[string][]byte{
				"key_a":       []byte("private-key-data"),
				"sshKeyB":     []byte("private-key-data"),
				"key_c":       []byte("private-key-data"),
				"keyD":        []byte("private-key-data"),
				"keyE":        []byte("private-key-data"),
				"known_hosts": []byte("known-host-data"),
			},
		},
	)

	noRemoteBase := applicationInfo{
		name:                 "app-foo",
		destinationNamespace: "foo",
	}

	defaultEnv := "GIT_SSH_COMMAND=ssh -q -F none -o IdentitiesOnly=yes -o IdentityFile=/dev/null -o UserKnownHostsFile=/dev/null -o StrictHostKeyChecking=no"
	env, err := setupGitSSH(context.Background(), withRemoteBaseTestDir, noRemoteBase)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(defaultEnv, env); diff != "" {
		t.Errorf("setupGitSSH()  mismatch (-want +got):\n%s", diff)
	}

	app := applicationInfo{
		name:                 "app-foo",
		destinationNamespace: "foo",
		gitSSHSecret: secretInfo{
			name: "argocd-git-ssh",
		},
	}

	wnatEnv := "GIT_SSH_COMMAND=ssh -q -F testData/app-with-remote-base-test1/.ssh/config -o UserKnownHostsFile=testData/app-with-remote-base-test1/.ssh/known_hosts"
	env, err = setupGitSSH(context.Background(), withRemoteBaseTestDir, app)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(wnatEnv, env); diff != "" {
		t.Errorf("setupGitSSH()  mismatch (-want +got):\n%s", diff)
	}

	// Application should contain following folders and files....
	expectedFiles := []string{
		".ssh",
		".ssh/config",
		".ssh/key_a",
		".ssh/sshKeyB",
		".ssh/key_c",
		".ssh/keyD",
		".ssh/keyE",
	}
	for _, name := range expectedFiles {
		p := filepath.Join(withRemoteBaseTestDir, name)
		_, err = os.Stat(p)
		if err != nil {
			t.Errorf("%s is missing, err:%s", p, err)
		}
	}

	// make sure kustomize files are updated...
	kustomize1, err := os.ReadFile(withRemoteBaseTestDir + "/kustomization.yaml")
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Contains(kustomize1, []byte("key_a_github_com")) {
		t.Error("github.com domain should be replaced by key_a_github_com")
	}
	if !bytes.Contains(kustomize1, []byte("keyD_github_com")) {
		t.Error("github.com domain should be replaced by keyD_github_com")
	}
	if !bytes.Contains(kustomize1, []byte("sshKeyB_gitlab_io")) {
		t.Error("gitlab.io domain should be replaced by sshKeyB_gitlab_io")
	}
	if !bytes.Contains(kustomize1, []byte("key_c_bitbucket_org")) {
		t.Error("bitbucket.org domain should be replaced by key_c_bitbucket_org")
	}

	kustomize2, err := os.ReadFile(withRemoteBaseTestDir + "/app/kustomization.yml")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(kustomize2, []byte("keyE_github_com")) {
		t.Error("github.com domain should be replaced by keyE_github_com")
	}
}
