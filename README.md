# argocd-strongbox-plugin

An Argo CD plugin to decrypt strongbox encrypted files and build Kubernetes resources. 
plugin supports argocd version from 2.4 onwards and only same cluster deployments are supported.

This plugin has 2 commands

### `decrypt` 
command will read kube secret containing keyring data and run strongbox decryption using this data. 
if multiple keys are used to encrypt app secrets then this secret should contain all the keys.

### `generate` 
command will run kustomize build to generate kube resources's yaml strings. it will print this yaml stream to stdout.

You can specify custom SSH keys to be used for fetching remote kustomize bases from private repositories. In order to do that, 
you will need to set `GIT_SSH_SECRET_NAME` plugin env, it should reference a Secret name that contains one or more SSH keys 
that provide access to the private repositories that contain these bases. if this env is not set then plugin will 
only be able to fetch remote bases from open repositories.

To use an SSH key for Kustomize bases, the bases should be defined with the ssh:// scheme in kustomization.yaml and have a 
`# argocd-strongbox-plugin: key_foobar` comment above it. For example:

```yaml
resources:
  # https scheme (default if omitted), any SSH keys defined are ignored
  - github.com/org/open1//manifests/lab-foo?ref=master

  # ssh scheme requires a valid SSH key to be defined
  # here keyA will be used to fetch repo1 and KeyB for repo2
  # argocd-strongbox-plugin: keyA
  - ssh://github.com/org/repo1//manifests/lab-foo?ref=master
  # argocd-strongbox-plugin: KeyB
  - ssh://github.com/org/repo2//manifests/lab-zoo?ref=dev
```

## Environment Variables

### Strongbox ENVs

 Plugin supports following _plugin envs_ which can be set in ArgoCD Application crd

`STRONGBOX_SECRET_NAME` the value should be the name of a secret resource containing strongbox keyring used to encrypt app secrets. the default value is `argocd-strongbox-keyring`

`STRONGBOX_SECRET_KEY` the value should be the name of the secret data key which contains a valid strongbox keyring file data. the default value is `.strongbox_keyring`

`STRONGBOX_SECRET_NAMESPACE` If you need to deploy a shared strongbox keyring to use in multiple namespaces, then it can be set by this ENV.
the Secret should have an annotation called "argocd-strongbox.plugin.io/allowed-namespaces" which contains a comma-separated list of all the namespaces that are allowed to use it.
Since ArgoCD Application can be used to create a namespace, wild card is not supported in the allow list. it is an exact matching.
If this env is not specified then it defaults to the same namespace as the app's destination NS.


```yaml
# secret example the following secret can be used by namespaces "ns-a", "ns-b" and "ns-c":
kind: Secret
apiVersion: v1
metadata:
  name: argocd-strongbox-keyring
  namespace: ns-a
  annotations:
    argocd-strongbox.plugin.io/allowed-namespaces: "ns-b, ns-c"
stringData:
  .strongbox_keyring: |-
    keyentries:
    - description: mykey
      key-id: AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA
      key: AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA
```

```yaml
# argocd application configuration
spec:
  source:
    repoURL: git@github.com/my-org/app.git
    targetRevision: HEAD
    plugin:
      env:
        - name: STRONGBOX_SECRET_NAMESPACE
          value: team-a
        - name: STRONGBOX_SECRET_NAME
          value: argocd-strongbox-keyring
        - name: STRONGBOX_SECRET_KEY
          value: .strongbox_keyring
```

### Git SSH Keys Envs

`GIT_SSH_SECRET_NAME` the value should be the name of a secret resource containing ssh keys used for fetching remote kustomize bases from private repositories. Additionally this Secret can optionally define a value for "known_hosts". If omitted, git will use ssh with StrictHostKeyChecking disabled. There is no default value for this env it must be set if repo base contains remote 
private bases. 

`GIT_SSH_SECRET_NAMESPACE` the value should be the name of a namespace where secret resource containing ssh keys are located. If this env is not specified then it defaults to the same namespace as the app's destination NS.
the Secret should have an annotation called "argocd-strongbox.plugin.io/allowed-namespaces" which contains a comma-separated list of all the namespaces that are allowed to use it.

```yaml
kind: Secret
apiVersion: v1
metadata:
  name: argocd-git-ssh
  namespace: ns-a
  annotations:
    kube-applier.io/allowed-namespaces: "ns-b, ns-c"
stringData:
  keyA: |-
    -----BEGIN OPENSSH PRIVATE KEY-----
    AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA==
    -----END OPENSSH PRIVATE KEY-----
  KeyB: |-
    -----BEGIN OPENSSH PRIVATE KEY-----
    AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA==
    -----END OPENSSH PRIVATE KEY-----
  known_hosts: |-
    github.com ssh-rsa AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA==
```

## Configuration and Installation plugin via sidecar 
for more details please see [argocd-docs](https://argo-cd.readthedocs.io/en/latest/user-guide/config-management-plugins/#option-2-configure-plugin-via-sidecar).


### 1. create plugin config file (asp.yaml) as shown, this file will be added to sidecar container using configMap.


```yaml
# cmp-plugin.yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: cmp-plugin
data:
  asp.yaml: |
    apiVersion: argoproj.io/v1alpha1
    kind: ConfigManagementPlugin
    metadata:
      name: argocd-strongbox-plugin
    spec:
      allowConcurrency: true
      discover:
        fileName: "*"
      init:
        command: 
          - argocd-strongbox-plugin
          - decrypt
        args:
          - "--secret-allowed-namespaces-annotation=argocd-strongbox.plugin.io/allowed-namespaces"
      generate:
        command:
          - argocd-strongbox-plugin
          - generate
        args:
          - "--secret-allowed-namespaces-annotation=argocd-strongbox.plugin.io/allowed-namespaces"
      lockRepo: false
```

### 2. patch `argocd-repo-server` deployment to add sidecar as shown
volume from `cmp-plugin` configMap and mount it to `/home/argocd/cmp-server/config/plugin.yaml`


```yaml
# argocd-repo-server-patch.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: argocd-repo-server
spec:
  template:
    spec:
      automountServiceAccountToken: true
      volumes:
        - configMap:
            name: cmp-plugin
          name: cmp-plugin
      containers:
        - name: asp
          image: quay.io/utilitywarehouse/argocd-strongbox-plugin:latest
          imagePullPolicy: Always
          command: [/var/run/argocd/argocd-cmp-server]
          securityContext:
            runAsNonRoot: true
            runAsUser: 999
          volumeMounts:
            - mountPath: /var/run/argocd
              name: var-files
            - mountPath: /home/argocd/cmp-server/plugins
              name: plugins
            - mountPath: /tmp
              name: tmp

            # Register plugins into sidecar
            - mountPath: /home/argocd/cmp-server/config/plugin.yaml
              subPath: asp.yaml
              name: cmp-plugin
```

Important notes from argocd docs
> 1. Make sure to use /var/run/argocd/argocd-cmp-server as an entrypoint. The argocd-cmp-server is a lightweight GRPC service that allows Argo CD to interact with the plugin.
> 2. Make sure that sidecar container is running as user 999.
> 3. Make sure that plugin configuration file is present at /home/argocd/cmp-server/config/plugin.yaml. It can either be volume mapped via configmap or baked into image.

### 3. Give `argocd-repo-server` serviceAccount read access to secrets from all or required namespace

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: argocd-strongbox-plugin
rules:
  - apiGroups: [""]
    resources: ["secrets"]
    verbs: ["get"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  labels:
  name: argocd-strongbox-plugin
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: argocd-strongbox-plugin
subjects:
  - kind: ServiceAccount
    name: argocd-repo-server
    namespace: sys-argocd

```



### Plugin Configuration 
#### decrypt

| app arguments/ENVs | default | example / explanation |
|-|-|-|
| --secret-allowed-namespaces-annotation | argocd-strongbox.plugin.io/allowed-namespaces | when shared secret is used this value is the annotation key to look for in secret to get comma-separated list of all the namespaces that are allowed to use it |
| ARGOCD_APP_NAME | set by argocd | name of application |
| ARGOCD_APP_NAMESPACE | set by argocd | application's destination namespace |
| STRONGBOX_SECRET_NAME¹ | argocd-strongbox-keyring | the name of a secret resource containing strongbox keyring used to encrypt app secrets |
| STRONGBOX_KEYRING_KEY¹ | .strongbox_keyring | the name of the secret data key which contains a valid strongbox keyring file |
| STRONGBOX_SECRET_NAMESPACE¹ | | the name of a namespace where secret resource containing strongbox keyring is located |

¹ These ENVs should be added to argocd application plugin env sections
