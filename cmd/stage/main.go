package main

import (
	"os"

	"github.com/jystringfellow/scoring-stage/internal/cli"
)

func main() {
	os.Exit(cli.New().Main(os.Args[1:]))
}
