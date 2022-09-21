package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/hashicorp/go-hclog"
	"github.com/urfave/cli/v2"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

var (
	// argocd adds `ARGOCD_ENV_` prefix to all ENVs configured in Applications
	argocdAppEnvPrefix = "ARGOCD_ENV_"

	logger = hclog.New(&hclog.LoggerOptions{
		Name: "argocd-strongbox-plugin",

		// when plugin commands are executed via repo server
		// logs are only printed if there is error while exec commands
		// logs are ignored if there plugin is exec successfully
		Level: hclog.Error,
	})
)

func main() {
	app := &cli.App{
		Commands: []*cli.Command{
			{
				Name:    "init",
				Aliases: []string{"a"},
				Usage:   "init will init",
				Action: func(cCtx *cli.Context) error {
					return nil
				},
			}, {
				Name:  "generate",
				Usage: "generate will decrypt all strongbox encrypted file and run kustomize build to generate kube resources",
				Action: func(cCtx *cli.Context) error {
					cwd, err := os.Getwd()
					if err != nil {
						return fmt.Errorf("unable to get current working dir err:%s", err)
					}

					// creates the in-cluster config
					config, err := rest.InClusterConfig()
					if err != nil {
						return fmt.Errorf("unable to create in-cluster config err:%s", err)
					}

					// creates the clientset
					clientset, err := kubernetes.NewForConfig(config)
					if err != nil {
						return fmt.Errorf("unable to create clientset err:%s", err)
					}

					app := getAppDetails()

					manifests, err := ensureBuild(cCtx.Context, clientset, cwd, app)
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

func getAppDetails() argoApp {
	app := argoApp{
		name:                 os.Getenv("ARGOCD_APP_NAME"),
		namespace:            os.Getenv("ARGOCD_APP_NAMESPACE"),
		revision:             os.Getenv("ARGOCD_APP_REVISION"),
		sourcePath:           os.Getenv("ARGOCD_APP_SOURCE_PATH"),
		sourceURL:            os.Getenv("ARGOCD_APP_SOURCE_REPO_URL"),
		sourceTargetRevision: os.Getenv("ARGOCD_APP_SOURCE_TARGET_REVISION"),
	}

	app.pluginEnvs = map[string]string{}

	// get all plugin envs configured in app resource
	for _, v := range os.Environ() {
		env := strings.Split(v, "=")
		if len(env) == 2 &&
			strings.HasPrefix(env[0], argocdAppEnvPrefix) {
			app.pluginEnvs[strings.TrimPrefix(env[0], argocdAppEnvPrefix)] = env[1]
		}
	}

	return app
}
