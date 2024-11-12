package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	"filippo.io/age/armor"
	v1 "k8s.io/api/core/v1"
	kErrors "k8s.io/apimachinery/pkg/api/errors"
	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var errNotFound = errors.New("not found")

// secret reads Kube Secret from either working NS or specified NS
// if different NS is used then it will verify that working NS is allowed to use that Secret
func secret(ctx context.Context, workingNamespace string, secret secretInfo) (*v1.Secret, error) {

	// if Secret Namespace is not set, then default to App's working Namespace
	if secret.namespace == "" {
		secret.namespace = workingNamespace
	}

	sec, err := kubeClient.CoreV1().Secrets(secret.namespace).Get(ctx, secret.name, metaV1.GetOptions{})
	if err != nil {
		if kErrors.IsNotFound(err) {
			return nil, fmt.Errorf("unable to get Secret: secret=%s namespace=%s err=%w", secret.namespace, secret.name, errNotFound)
		}
		return nil, fmt.Errorf("unable to get Secret: secret=%s namespace=%s err=%w", secret.namespace, secret.name, err)
	}

	// check if working Application is allowed to use Secret form another Namespace
	if secret.namespace != workingNamespace {
		for _, v := range strings.Split(sec.Annotations[allowedNamespacesSecretAnnotation], ",") {
			if strings.TrimSpace(v) == workingNamespace {
				return verifySecretEncrypted(sec)
			}
		}
		return nil, fmt.Errorf(`not allowed to use Secret, working Namespace missing from annotation: annotation=%s secretNamespace=%s secretName=%s workingNamespace=%s`,
			allowedNamespacesSecretAnnotation, secret.namespace, secret.name, workingNamespace)
	}

	return verifySecretEncrypted(sec)
}

// verifySecretEncrypted will go through all keys of the secret passed
// and error out if at least one of them is encrypted
func verifySecretEncrypted(sec *v1.Secret) (*v1.Secret, error) {
	for k, v := range sec.Data {
		if bytes.HasPrefix(v, encryptedFilePrefix) || strings.HasPrefix(string(v), armor.Header) {
			return nil, fmt.Errorf("Secret contains encrypted data: namespace=%s name=%s key=%s", sec.Namespace, sec.Name, k)
		}
	}

	return sec, nil
}
