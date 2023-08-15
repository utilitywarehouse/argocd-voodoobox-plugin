# argocd-voodoobox-plugin

An Argo CD plugin to decrypt strongbox encrypted files and build Kubernetes resources. 
plugin supports argocd version from 2.4 onwards and only same cluster deployments are supported.

This plugin has 1 command

### `generate` 
generate command does following 2 things

1) it will read kube secret containing keyring data and run strongbox decryption using this data. 
if multiple keys are used to encrypt app secrets then this secret should contain all the keys.

2) command will run kustomize build to generate kube resources's yaml strings. it will print this yaml stream to stdout.

#### private repository

To fetch remote base from private repository, admin can add global ssh key which will be used for ALL applications.


user can also provide own ssh keys for an applications via secret with name `argocd-voodoobox-git-ssh`, 
that contains one or more SSH keys that provide access to the private repositories that contain these bases. To use an SSH key for Kustomize bases, 
the bases URL should be defined with the ssh:// scheme in kustomization.yaml and have a `# argocd-voodoobox-plugin: <key_file_name>` comment above it.
if only 1 ssh key is used for ALL private repos then there is no need to specify this comment. 

If `ssh://` is not used then plugin will assume only public repos are used and it will skip ssh config setup.

```yaml
resources:
  # https scheme (default if omitted), any SSH keys defined are ignored
  - github.com/org/open1//manifests/lab-foo?ref=master

  # ssh scheme requires a valid SSH key to be defined
  # here keyA will be used to fetch repo1 and KeyB for repo2
  # argocd-voodoobox-plugin: keyA
  - ssh://github.com/org/repo1//manifests/lab-foo?ref=master
  # argocd-voodoobox-plugin: KeyB
  - ssh://github.com/org/repo2//manifests/lab-zoo?ref=dev
```

## Environment Variables

### Strongbox ENVs

 Plugin supports following _plugin envs_ which can be set in ArgoCD Application crd

The value of name of a secret resource containing strongbox keyring used to encrypt app secrets, must be `argocd-voodoobox-strongbox-keyring`.

`STRONGBOX_SECRET_KEY` the value should be the name of the secret data key which contains a valid strongbox keyring file data. the default value is `.strongbox_keyring`

`STRONGBOX_SECRET_NAMESPACE` If you need to deploy a shared strongbox keyring to use in multiple namespaces, then it can be set by this ENV.
the Secret should have an annotation called "argocd.voodoobox.plugin.io/allowed-namespaces" which contains a comma-separated list of all the namespaces that are allowed to use it.
Since ArgoCD Application can be used to create a namespace, wild card is not supported in the allow list. it is an exact matching.
If this env is not specified then it defaults to the same namespace as the app's destination NS.


```yaml
# secret example the following secret can be used by namespaces "ns-a", "ns-b" and "ns-c":
kind: Secret
apiVersion: v1
metadata:
  name: argocd-voodoobox-strongbox-keyring
  namespace: ns-a
  annotations:
    argocd.voodoobox.plugin.io/allowed-namespaces: "ns-b, ns-c"
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
        - name: STRONGBOX_SECRET_KEY
          value: .strongbox_keyring
```

### Git SSH Keys Envs

`GIT_SSH_SECRET_NAMESPACE` the value should be the name of a namespace where secret resource containing ssh keys are located. If this env is not specified then it defaults to the same namespace as the app's destination NS.
the Secret should have an annotation called "argocd.voodoobox.plugin.io/allowed-namespaces" which contains a comma-separated list of all the namespaces that are allowed to use it.

```yaml
kind: Secret
apiVersion: v1
metadata:
  name: argocd-voodoobox-git-ssh
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
      name: argocd-voodoobox-plugin
    spec:
      allowConcurrency: true
      discover:
        fileName: "*"
      generate:
        command:
          - argocd-voodoobox-plugin
          - generate
        args:
          - "--global-git-ssh-key-file=/path/to/global/key"
          - "--global-git-ssh-known-hosts-file=/path/to/global/khf"
          - "--app-strongbox-secret-name=argocd-voodoobox-strongbox-keyring"
          - "--app-git-ssh-secret-name=argocd-voodoobox-git-ssh"
          - "--allowed-namespaces-secret-annotation=argocd.voodoobox.plugin.io/allowed-namespaces"
      lockRepo: false
```
* Instead of setting up arguments via configMap we can also set corresponding ENVs on plugin side car 

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
          image: quay.io/utilitywarehouse/argocd-voodoobox-plugin:latest
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
  name: argocd-voodoobox-plugin
rules:
  - apiGroups: [""]
    resources: ["secrets"]
    resourceNames:
      - argocd-voodoobox-strongbox-keyring
      - argocd-voodoobox-git-ssh
    verbs: ["get"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  labels:
  name: argocd-voodoobox-plugin
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: argocd-voodoobox-plugin
subjects:
  - kind: ServiceAccount
    name: argocd-repo-server
    namespace: sys-argocd

```



### Plugin Configuration 
#### decrypt

| app arguments/ENVs | default | example / explanation |
|-|-|-|
| --allowed-namespaces-secret-annotation | argocd.voodoobox.plugin.io/allowed-namespaces | when shared secret is used this value is the annotation key to look for in secret to get comma-separated list of all the namespaces that are allowed to use it |
| --global-git-ssh-key-file | | The path to git ssh key file which will be used as global ssh key to fetch kustomize base from private repo for all application |
| --global-git-ssh-known-hosts-file | | The path to git known hosts file which will be used as with global ssh key to fetch kustomize base from private repo for all application |
| --app-strongbox-secret-name | argocd-voodoobox-strongbox-keyring | the value should be the name of a secret resource containing strongbox keyring used to encrypt app secrets. name will be same across all applications |
| --app-git-ssh-secret-name | argocd-voodoobox-git-ssh | the value should be the name of a secret resource containing ssh keys used for fetching remote kustomize bases from private repositories. name will be same across all applications |
| ARGOCD_APP_NAME | set by argocd | name of application |
| ARGOCD_APP_NAMESPACE | set by argocd | application's destination namespace |
| STRONGBOX_KEYRING_KEY¹ | .strongbox_keyring | the name of the secret data key which contains a valid strongbox keyring file |
| STRONGBOX_SECRET_NAMESPACE¹ | | the name of a namespace where secret resource containing strongbox keyring is located |
| GIT_SSH_SECRET_NAMESPACE¹ | | the value should be the name of a namespace where secret resource containing ssh keys are located |

¹ These ENVs should be added to argocd application plugin env sections
