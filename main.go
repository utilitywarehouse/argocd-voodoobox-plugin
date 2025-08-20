package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/urfave/cli/v2"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

const (
	// argocd adds `ARGOCD_ENV_` prefix to all plugin envs configured in Applications
	argocdAppEnvPrefix = "ARGOCD_ENV_"
)

var (
	kubeClient                        kubernetes.Interface
	allowedNamespacesSecretAnnotation string

	logger = hclog.New(&hclog.LoggerOptions{
		Name: "argocd-voodoobox-plugin",
	})
)

type applicationInfo struct {
	name                 string
	destinationNamespace string
	keyringSecret        secretInfo
	gitSSHSecret         secretInfo
}

type secretInfo struct {
	namespace string
	name      string
}

var flags = []cli.Flag{
	// following 2 ENVs are set by argo-cd while running plugin commands
	// https://argo-cd.readthedocs.io/en/latest/user-guide/build-environment
	// app-name is set by argo-cd as '<namespace>_<app-name>'
	&cli.StringFlag{
		Name:     "app-name",
		EnvVars:  []string{"ARGOCD_APP_NAME"},
		Usage:    "name of application ENV set by argocd",
		Required: true,
	},
	&cli.StringFlag{
		Name:     "app-namespace",
		EnvVars:  []string{"ARGOCD_APP_NAMESPACE"},
		Usage:    "destination application namespace ENV set by argocd",
		Required: true,
	},

	// following flags/envs should be set by admin as part of plugin config
	// Global SSH key
	&cli.StringFlag{
		Name:    "global-git-ssh-key-file",
		EnvVars: []string{"AVP_GLOBAL_GIT_SSH_KEY_FILE"},
		Usage:   "The path to git ssh key file which will be used as global ssh key to fetch kustomize base from private repo for all application",
	},
	&cli.StringFlag{
		Name:    "global-git-ssh-known-hosts-file",
		EnvVars: []string{"AVP_GLOBAL_GIT_SSH_KNOWN_HOSTS_FILE"},
		Usage:   "The path to git known hosts file which will be used as with global ssh key to fetch kustomize base from private repo for all application",
	},
	&cli.StringFlag{
		Name:    "allowed-namespaces-secret-annotation",
		EnvVars: []string{"AVP_ALLOWED_NS_SECRET_ANNOTATION"},
		Usage: `when shared secret is used this value is the annotation key to look for in secret 
to get comma-separated list of all the namespaces that are allowed to use it`,
		Destination: &allowedNamespacesSecretAnnotation,
		Value:       "argocd.voodoobox.plugin.io/allowed-namespaces",
	},

	// following envs comes from argocd application resource
	// strongbox secrets flags
	&cli.StringFlag{
		Name:    "app-strongbox-secret-namespace",
		EnvVars: []string{argocdAppEnvPrefix + "STRONGBOX_SECRET_NAMESPACE"},
		Usage: `set 'STRONGBOX_SECRET_NAMESPACE' in argocd application as plugin ENV. the value should be the
name of a namespace where secret resource containing strongbox keyring is located`,
	},
	// do not set `EnvVars` for secret name flag
	// To keep service account's permission minimum, the name of the secret is static across ALL applications.
	// this value should only be set by admins of argocd as part of plugin setup
	&cli.StringFlag{
		Name: "app-strongbox-secret-name",
		Usage: `the value should be the name of a secret resource containing strongbox keyring used to 
encrypt app secrets. name will be same across all applications`,
		Value: "argocd-voodoobox-strongbox-keyring",
	},

	// SSH secrets flags
	&cli.BoolFlag{
		Name:    "app-git-ssh-enabled",
		EnvVars: []string{argocdAppEnvPrefix + "GIT_SSH_CUSTOM_KEY_ENABLED"},
		Usage: `set 'GIT_SSH_CUSTOM_KEY_ENABLED' in ArgoCD application as plugin
		ENV. If set to "true" will use default values to lookup the
		Git SSH secret and use it.`,
	},
	&cli.StringFlag{
		Name:    "app-git-ssh-secret-namespace",
		EnvVars: []string{argocdAppEnvPrefix + "GIT_SSH_SECRET_NAMESPACE"},
		Usage: `set 'GIT_SSH_SECRET_NAMESPACE' in argocd application as plugin ENV. the value should be the
name of a namespace where secret resource containing ssh keys are located`,
	},
	// do not set `EnvVars` for secret name flag
	// To keep service account's permission minimum, the name of the secret is static across ALL applications.
	// this value should only be set by admins of argocd as part of plugin setup
	&cli.StringFlag{
		Name: "app-git-ssh-secret-name",
		Usage: `the value should be the name of a secret resource containing ssh keys used for 
fetching remote kustomize bases from private repositories. name will be same across all applications`,
		Value: "argocd-voodoobox-git-ssh",
	},
}

func main() {
	app := &cli.App{
		Commands: []*cli.Command{
			{
				Name:  "generate",
				Usage: "generate will decrypt all strongbox encrypted file and then run kustomize build to generate kube manifests",
				Flags: flags,
				Action: func(c *cli.Context) error {
					cwd, err := os.Getwd()
					if err != nil {
						return fmt.Errorf("unable to get current working dir err:%s", err)
					}

					kubeClient, err = getKubeClient()
					if err != nil {
						return fmt.Errorf("unable to create kube clienset err:%s", err)
					}

					globalKeyPath := c.String("global-git-ssh-key-file")
					globalKnownHostFile := c.String("global-git-ssh-known-hosts-file")

					app := applicationInfo{
						name:                 c.String("app-name"),
						destinationNamespace: c.String("app-namespace"),
					}

					logger = logger.With("app", app.name)

					if c.Bool("app-git-ssh-enabled") {
						app.gitSSHSecret = secretInfo{
							name:      c.String("app-git-ssh-secret-name"),
							namespace: c.String("app-git-ssh-secret-namespace"),
						}
					}
					start := time.Now()

					logger.Info("starting decryption")

					// Always try to decrypt
					app.keyringSecret = secretInfo{
						name:      c.String("app-strongbox-secret-name"),
						namespace: c.String("app-strongbox-secret-namespace"),
					}
					if err := ensureDecryption(c.Context, cwd, app); err != nil {
						return fmt.Errorf("decryption error: duration:%s error:%w", time.Since(start), err)
					}
					decryptTime := time.Since(start)
					logger.Info("starting build", "decryption-duration", decryptTime)

					manifests, err := ensureBuild(c.Context, cwd, globalKeyPath, globalKnownHostFile, app)
					if err != nil {
						return fmt.Errorf("build error: duration:%s error:%w", time.Since(start), err)
					}
					logger.Info("build done", "decryption-duration", decryptTime, "total-duration", time.Since(start))

					// argocd creates a temp folder of plugin which gets deleted
					// once plugin is existed still clean up secrets manually
					// in case this behaviour changes
					os.Remove(filepath.Join(cwd, strongboxKeyringFilename))
					os.RemoveAll(filepath.Join(cwd, ".ssh"))

					fmt.Printf("%s", manifests)
					return nil
				},
			},
		},
	}

	if err := app.Run(os.Args); err != nil {
		logger.Error("app terminated", "err", err)
		os.Exit(1)
	}
}

func getKubeClient() (*kubernetes.Clientset, error) {
	// creates the in-cluster config
	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("unable to create in-cluster config err:%s", err)
	}

	// creates the clientset
	return kubernetes.NewForConfig(config)
}
