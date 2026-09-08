// Command assistant-contracts refreshes request schemas used by the embedded
// administrator assistant. Run from apps/api-go with go run ./cmd/assistant-contracts.
package main

import (
	"flag"
	"log"

	"github.com/LIghtJUNction/api.lmm.best/internal/assistantcontracts"
)

func main() {
	root := flag.String("root", ".", "Go module source directory")
	flag.Parse()
	if err := assistantcontracts.Write(*root); err != nil {
		log.Fatal(err)
	}
}
