package main

import (
	"fmt"
	"log"
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
	appName      string
	appNamespace string

	clientset *kubernetes.Clientset

	logger = hclog.New(&hclog.LoggerOptions{
		Name: "argocd-strongbox-plugin",

		// when plugin commands are executed by repo server
		// logs are only printed if there is an error while executing a command
		// logs are ignored if plugin command is executed successfully
		Level: hclog.Error,
	})
)

// following ENVs are set by argo-cd while running plugin commands
// https://argo-cd.readthedocs.io/en/latest/user-guide/build-environment
var commonFlags = []cli.Flag{
	// app-name is set by argo-cd as '<namespace>_<app-name>'
	&cli.StringFlag{
		Name:        "app-name",
		EnvVars:     []string{"ARGOCD_APP_NAME"},
		Usage:       "name of application",
		Required:    true,
		Destination: &appName,
	},
	&cli.StringFlag{
		Name:        "app-namespace",
		EnvVars:     []string{"ARGOCD_APP_NAMESPACE"},
		Usage:       "destination application namespace.",
		Required:    true,
		Destination: &appNamespace,
	},
}

func init() {
	// creates the in-cluster config
	config, err := rest.InClusterConfig()
	if err != nil {
		log.Fatalf("unable to create in-cluster config err:%s", err)
	}

	// creates the clientset
	clientset, err = kubernetes.NewForConfig(config)
	if err != nil {
		log.Fatalf("unable to create clientset err:%s", err)
	}
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
						Name:    "app-strongbox-secret-name",
						EnvVars: []string{argocdAppEnvPrefix + "STRONGBOX_SECRET_NAME"},
						Usage: `set 'STRONGBOX_SECRET_NAME' in argocd application as plugin ENV. the value should be the
						name of a secret resource containing strongbox keyring used to encrypt app secrets`,
						Value: "argocd-strongbox-keyring",
					},
					&cli.StringFlag{
						Name:    "app-strongbox-keyring-key",
						EnvVars: []string{argocdAppEnvPrefix + "STRONGBOX_KEYRING_KEY"},
						Usage: `set 'STRONGBOX_KEYRING_KEY' in argocd application as plugin ENV, the value should be the
						name of the key which contains a valid strongbox keyring file`,
						Value: ".strongbox_keyring",
					},
				}...),
				Action: func(c *cli.Context) error {
					cwd, err := os.Getwd()
					if err != nil {
						return fmt.Errorf("unable to get current working dir err:%s", err)
					}

					found, err := hasEncryptedFiles(cwd)
					if err != nil {
						return fmt.Errorf("unable to check if app source folder has encrypted files err:%s", err)
					}

					if !found {
						return nil
					}

					return ensureDecryption(c.Context, cwd, c.String("app-strongbox-secret-name"), c.String("app-strongbox-keyring-key"))
				},
			},

			// command to generate manifests YAML
			{
				Name:  "generate",
				Usage: "generate will run kustomize build to generate kube manifests",
				Flags: commonFlags,
				Action: func(cCtx *cli.Context) error {
					cwd, err := os.Getwd()
					if err != nil {
						return fmt.Errorf("unable to get current working dir err:%s", err)
					}

					manifests, err := runKustomizeBuild(cCtx.Context, cwd)
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
