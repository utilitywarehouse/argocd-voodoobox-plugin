package main

type argoApp struct {
	name                 string
	namespace            string
	revision             string
	sourcePath           string
	sourceURL            string
	sourceTargetRevision string
	pluginEnvs           map[string]string
}
