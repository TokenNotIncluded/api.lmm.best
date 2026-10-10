package main

import (
	"log"

	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/app"
)

func main() {
	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
