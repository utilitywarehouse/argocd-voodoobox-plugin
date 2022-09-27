package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func Test_ensureBuild(t *testing.T) {

	manifests, err := ensureBuild(context.Background(), plainTextTestDir)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println(manifests)

	if !(strings.Contains(manifests, "kind: Deployment") &&
		strings.Contains(manifests, "name: app-bar-nginx") &&
		strings.Contains(manifests, "namespace: lab-bar")) {
		t.Fatal("required details not found in generated manifests")
	}
}
