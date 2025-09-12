package main

import (
	"dv/internal/cli"
	"log"
)

var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

func main() {
	if err := cli.Execute(); err != nil {
		log.Fatal(err)
	}
}
