//go:build linux

// An owner-operated recovery executable, never an HTTP server or package installer.
package main

import (
	"github.com/LIghtJUNction/api.lmm.best/internal/appcli"
	"os"
)

func main() { os.Exit(appcli.RunIncident20260920Recovery(os.Args[1:], os.Stdout, os.Stderr)) }
