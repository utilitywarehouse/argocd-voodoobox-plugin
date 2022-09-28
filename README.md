# argocd-strongbox-plugin

An Argo CD plugin to decrypt strongbox encrypted files and build Kubernetes resources. plugin only supports argocd version from 2.4 onwards

This plugin has 2 commands

### `decrypt` 
command will read kube secret containing keyring data from app's target namespace and run strongbox decryption using this data.
if multiple keys are used to encrypt app secrets then this secret should contain all the keys.

### `generate` 
command will run kustomize build to generate kube resources's yaml strings. it will print this yaml stream to stdout.

## Environment Variables

 Plugin supports following _plugin envs_ which can be set in ArgoCD Application crd

`STRONGBOX_SECRET_NAME` the value should be the name of a secret resource containing strongbox keyring used to encrypt app secrets. the default value is `argocd-strongbox-keyring`

`STRONGBOX_SECRET_KEY` the value should be the name of the secret data key which contains a valid strongbox keyring file data. the default value is `.strongbox_keyring`

```yaml
spec:
  source:
    repoURL: git@github.com/my-org/app.git
    targetRevision: HEAD
    plugin:
      env:
        - name: STRONGBOX_SECRET_NAME
          value: strongbox-keyring
        - name: STRONGBOX_SECRET_KEY
          value: keyring
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
      generate:
        command:
          - argocd-strongbox-plugin
          - generate
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
