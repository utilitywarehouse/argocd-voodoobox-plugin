package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/hashicorp/go-hclog"
	"github.com/urfave/cli/v2"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

const (
	// argocd adds `ARGOCD_ENV_` prefix to all plugin envs configured in Applications
	argocdAppEnvPrefix = "ARGOCD_ENV_"

	strongboxKeyRingFile = ".strongbox_keyring"
	SSHDirName           = ".ssh"
)

var (
	kubeClient                        kubernetes.Interface
	allowedNamespacesSecretAnnotation string

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
		Value:   "/etc/git-secret/ssh",
		Usage:   "The path to git ssh key which will be used to setup GIT_SSH_COMMAND env.",
	},
	&cli.StringFlag{
		Name:    "global-git-ssh-known-hosts-file",
		EnvVars: []string{"AVP_GLOBAL_GIT_SSH_KNOWN_HOSTS_FILE"},
		Value:   "/etc/git-secret/known_hosts",
		Usage:   "The local path to the known hosts file used to setup GIT_SSH_COMMAND env.",
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
		Value: "argocd-voodoobox-strongbox-keyring",
	},

	// SSH secrets flags
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
					app.keyringSecret = secretInfo{
						namespace: c.String("app-strongbox-secret-namespace"),
						name:      c.String("app-strongbox-secret-name"),
						key:       c.String("app-strongbox-secret-key"),
					}
					app.gitSSHSecret = secretInfo{
						namespace: c.String("app-git-ssh-secret-namespace"),
						name:      c.String("app-git-ssh-secret-name"),
					}

					if err := ensureDecryption(c.Context, cwd, app); err != nil {
						return err
					}

					manifests, err := ensureBuild(c.Context, cwd, globalKeyPath, globalKnownHostFile, app)
					if err != nil {
						return err
					}

					// argocd creates a temp folder of plugin which gets deleted
					// once plugin is existed still clean up secrets manually
					// in case this behavior changes
					os.Remove(filepath.Join(cwd, strongboxKeyRingFile))
					os.RemoveAll(filepath.Join(cwd, SSHDirName))

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
