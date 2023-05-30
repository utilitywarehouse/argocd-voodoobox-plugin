package main

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	v1 "k8s.io/api/core/v1"
	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// getSecret reads kube secret from either destination NS or secret's NS
// if different NS is used then it will verify that dest NS is allowed to read the secret
func getSecret(ctx context.Context, destNamespace string, secret secretInfo) (*v1.Secret, error) {

	// if secret namespace is not set then default to app's destination namespace
	if secret.namespace == "" || secret.namespace == destNamespace {
		sec, err := kubeClient.CoreV1().Secrets(destNamespace).Get(ctx, secret.name, metaV1.GetOptions{})
		if err != nil {
			return nil, fmt.Errorf("unable to get secret %s/%s err:%s", destNamespace, secret.name, err)
		}
		return isSecretEncrypted(sec)
	}

	sec, err := kubeClient.CoreV1().Secrets(secret.namespace).Get(ctx, secret.name, metaV1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("unable to get secret %s/%s err:%s", secret.namespace, secret.name, err)
	}

	// check if app's destination namespace is allowed on given secret resource
	for _, v := range strings.Split(sec.Annotations[secretAllowedNamespacesAnnotation], ",") {
		if strings.TrimSpace(v) == destNamespace {
			return isSecretEncrypted(sec)
		}
	}

	return nil, fmt.Errorf(`secret "%s/%s" cannot be used in namespace "%s", the destination namespace must be listed in the '%s' annotation`,
		secret.namespace, secret.name, destNamespace, secretAllowedNamespacesAnnotation)
}

// isSecretEncrypted will go through all keys of the secret passed
// and error out if at least one of them is encrypted
func isSecretEncrypted (sec *v1.Secret) (*v1.Secret, error) {
	for k, v := range sec.Data{
		if bytes.Contains(v, encryptedFilePrefix) {
			return nil, fmt.Errorf("secret %s/%s has an encrypted data for the key %s", sec.Namespace, sec.Name, k)
		}
	}

	return sec, nil
}
