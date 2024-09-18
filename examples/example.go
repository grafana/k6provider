// Package main is an example of how to use k6provider
package main

import (
	"context"
	"fmt"
	"os/exec"

	"github.com/grafana/k6deps"
	"github.com/grafana/k6provider"
)

// Main is an example of how to use k6provider to obtain a k6 binary based on a set of dependencies,
// and execute it to check its version.
func main() {
	// get a k6 provider configured with a build service defined in K6_BUILD_SERVICE_URL
	provider, err := k6provider.NewDefaultProvider()
	if err != nil {
		panic(err)
	}

	// create dependencies for k6 version v0.52.0
	deps := make(k6deps.Dependencies)
	err = deps.UnmarshalText([]byte("k6=v0.52.0"))
	if err != nil {
		panic(err)
	}

	// obtain binary from build service
	k6binary, err := provider.GetBinary(context.TODO(), deps)
	if err != nil {
		panic(err)
	}

	// execute k6 binary and check version
	cmd := exec.Command(k6binary.Path, "version")
	out, err := cmd.Output()
	if err != nil {
		panic(err)
	}

	fmt.Print(string(out))
}
