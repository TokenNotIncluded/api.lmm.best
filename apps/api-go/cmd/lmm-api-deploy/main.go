// lmm-api-deploy-engine is built and shipped separately from the API server.
package main

import (
	"fmt"
	"os"

	"github.com/LIghtJUNction/api.lmm.best/internal/deploycli"
)

var version = "dev"

func main() {
	args := os.Args[1:]
	if len(args) == 1 && (args[0] == "version" || args[0] == "--version") {
		fmt.Fprintln(os.Stdout, version)
		return
	}
	// Retained native transaction records use this private protocol prefix.
	// It is accepted by the deployment executable, never by the API server.
	if len(args) > 0 && args[0] == "operator" {
		args = args[1:]
	}
	os.Exit(deploycli.RunDeploy(args, os.Stdout, os.Stderr))
}
