package main

import (
	"fmt"
	"os"

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
	secretAllowedNamespacesAnnotation string

	logger = hclog.New(&hclog.LoggerOptions{
		Name: "argocd-voodoobox-plugin",

		// when plugin commands are executed by repo server
		// logs are only printed if there is an error while executing a command
		// logs are ignored if plugin command is executed successfully
		Level: hclog.Error,
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
	key       string
}

// following ENVs are set by argo-cd while running plugin commands
// https://argo-cd.readthedocs.io/en/latest/user-guide/build-environment
var commonFlags = []cli.Flag{
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
}

func main() {
	app := &cli.App{
		Commands: []*cli.Command{
			// command to initialise application source directory
			{
				Name:  "decrypt",
				Usage: "command to decrypt all encrypted files under application source directory",
				Flags: append(commonFlags, []cli.Flag{
					&cli.StringFlag{
						Name:    "app-strongbox-secret-namespace",
						EnvVars: []string{argocdAppEnvPrefix + "STRONGBOX_SECRET_NAMESPACE"},
						Usage: `set 'STRONGBOX_SECRET_NAMESPACE' in argocd application as plugin ENV. the value should be the
	name of a namespace where secret resource containing strongbox keyring is located`,
					},
					&cli.StringFlag{
						Name:    "app-strongbox-secret-key",
						EnvVars: []string{argocdAppEnvPrefix + "STRONGBOX_SECRET_KEY"},
						Usage: `set 'STRONGBOX_KEYRING_KEY' in argocd application as plugin ENV, the value should be the
	name of the secret data key which contains a valid strongbox keyring file`,
						Value: ".strongbox_keyring",
					},
					// do not set `EnvVars` for secret name flag
					// To keep service account's permission minimum, the name of the secret is static across ALL applications.
					// this value should only be set by admins of argocd as part of plugin setup
					&cli.StringFlag{
						Name: "app-strongbox-secret-name",
						Usage: `the value should be the name of a secret resource containing strongbox keyring used to 
encrypt app secrets. name will be same across all applications`,
						Value: "argocd-strongbox-keyring",
					},
					&cli.StringFlag{
						Name: "secret-allowed-namespaces-annotation",
						Usage: `when shared secret is used this value is the annotation key to look for in secret 
	to get comma-separated list of all the namespaces that are allowed to use it`,
						Destination: &secretAllowedNamespacesAnnotation,
						Value:       "argocd.voodoobox.plugin.io/allowed-namespaces",
					},
				}...),
				Action: func(c *cli.Context) error {
					var err error
					app := applicationInfo{
						name:                 c.String("app-name"),
						destinationNamespace: c.String("app-namespace"),
					}
					app.keyringSecret = secretInfo{
						namespace: c.String("app-strongbox-secret-namespace"),
						name:      c.String("app-strongbox-secret-name"),
						key:       c.String("app-strongbox-secret-key"),
					}

					kubeClient, err = getKubeClient()
					if err != nil {
						return fmt.Errorf("unable to create kube clienset err:%s", err)
					}

					cwd, err := os.Getwd()
					if err != nil {
						return fmt.Errorf("unable to get current working dir err:%s", err)
					}

					return ensureDecryption(c.Context, cwd, app)
				},
			},

			// command to generate manifests YAML
			{
				Name:  "generate",
				Usage: "generate will run kustomize build to generate kube manifests",
				Flags: append(commonFlags, []cli.Flag{
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
					&cli.StringFlag{
						Name: "secret-allowed-namespaces-annotation",
						Usage: `when shared secret is used this value is the annotation key to look for in secret 
	to get comma-separated list of all the namespaces that are allowed to use it`,
						Destination: &secretAllowedNamespacesAnnotation,
						Value:       "argocd.voodoobox.plugin.io/allowed-namespaces",
					},
				}...),
				Action: func(c *cli.Context) error {
					cwd, err := os.Getwd()
					if err != nil {
						return fmt.Errorf("unable to get current working dir err:%s", err)
					}

					kubeClient, err = getKubeClient()
					if err != nil {
						return fmt.Errorf("unable to create kube clienset err:%s", err)
					}

					app := applicationInfo{
						name:                 c.String("app-name"),
						destinationNamespace: c.String("app-namespace"),
					}
					app.gitSSHSecret = secretInfo{
						namespace: c.String("app-git-ssh-secret-namespace"),
						name:      c.String("app-git-ssh-secret-name"),
					}

					manifests, err := ensureBuild(c.Context, cwd, app)
					if err != nil {
						return err
					}

					fmt.Printf("%s\n---\n", manifests)
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
