package main

import (
	"dv/internal/cli"
	"log"
)

func main() {
	if err := cli.Execute(); err != nil {
		log.Fatal(err)
	}
}
