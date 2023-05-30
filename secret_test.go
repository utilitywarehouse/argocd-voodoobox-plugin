package main

import (
	"context"
	"reflect"
	"testing"

	v1 "k8s.io/api/core/v1"
	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func Test_getSecret(t *testing.T) {
	secretAllowedNamespacesAnnotation = "argocd.voodoobox.plugin.io/allowed-namespaces"

	kubeClient = fake.NewSimpleClientset(
		&v1.Secret{
			ObjectMeta: metaV1.ObjectMeta{
				Name:      "argocd-strongbox-secret",
				Namespace: "bar",
			},
		},
		&v1.Secret{
			ObjectMeta: metaV1.ObjectMeta{
				Name:      "strongbox-secret",
				Namespace: "foo",
				Annotations: map[string]string{
					"argocd.voodoobox.plugin.io/allowed-namespaces": "bar,baz",
				},
			},
			Data: map[string][]byte{
				".strongbox_keyring": []byte(`keyentries:
- description: foo-key
key-id: xxxxxxxxx
key: xxxxxxxxx`),
			},
		},
		&v1.Secret{
			ObjectMeta: metaV1.ObjectMeta{
				Name:      "strongbox-secret",
				Namespace: "baz",
			},
		},
		&v1.Secret{
			ObjectMeta: metaV1.ObjectMeta{
				Name:      "strongbox-secret",
				Namespace: "enc-bar",
			},
			Data: map[string][]byte{
				".secret-file": []byte(`# STRONGBOX ENCRYPTED RESOURCE ; See https://github.com/uw-labs/strongbox
xxxxxxxxx`),
				"secret2": []byte("unencrypted data"),
			},
		},
	)

	type args struct {
		destNamespace string
		secret        secretInfo
	}
	tests := []struct {
		name    string
		args    args
		want    *v1.Secret
		wantErr bool
	}{
		{
			"no secret ns",
			args{destNamespace: "bar", secret: secretInfo{name: "argocd-strongbox-secret", namespace: ""}},
			&v1.Secret{ObjectMeta: metaV1.ObjectMeta{Name: "argocd-strongbox-secret", Namespace: "bar"}},
			false,
		},
		{
			"secret ns same as destination ns",
			args{destNamespace: "bar", secret: secretInfo{name: "argocd-strongbox-secret", namespace: "bar"}},
			&v1.Secret{ObjectMeta: metaV1.ObjectMeta{Name: "argocd-strongbox-secret", Namespace: "bar"}},
			false,
		},
		{
			"secret ns different from destination ns (with annotation)",
			args{destNamespace: "bar", secret: secretInfo{name: "strongbox-secret", namespace: "foo"}},
			&v1.Secret{
				ObjectMeta: metaV1.ObjectMeta{
					Name: "strongbox-secret", Namespace: "foo",
					Annotations: map[string]string{"argocd.voodoobox.plugin.io/allowed-namespaces": "bar,baz"},
				},
				Data: map[string][]byte{
					".strongbox_keyring": []byte(`keyentries:
- description: foo-key
key-id: xxxxxxxxx
key: xxxxxxxxx`),
				},
			},
			false,
		},
		{
			"secret ns different from destination ns (without annotation)",
			args{destNamespace: "bar", secret: secretInfo{name: "strongbox-secret", namespace: "baz"}},
			nil,
			true,
		},
		{
			"sec ns missing secret",
			args{destNamespace: "bar", secret: secretInfo{name: "strongbox-secret", namespace: "bazz"}},
			nil,
			true,
		},
		{
			"secret is encrypted",
			args{destNamespace: "enc-bar", secret: secretInfo{name: "strongbox-secret", namespace: "enc-bar"}},
			nil,
			true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := getSecret(context.Background(), tt.args.destNamespace, tt.args.secret)
			if (err != nil) != tt.wantErr {
				t.Errorf("getSecret() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("getSecret() = %v, want %v", got, tt.want)
			}
		})
	}
}
