// Command coachctl is a single-user coaching CLI for Claude Code.
package main

import (
	"os"

	"github.com/OliverKrr/coachctl/internal/cli"
)

// version is stamped by the release build (-ldflags "-X main.version=…").
var version = "dev"

func main() {
	os.Exit(cli.Main(version, os.Args[1:]))
}
