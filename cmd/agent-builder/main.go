// Command agent-builder is the entrypoint for the autonomous block-building agent.
package main

import (
	"os"

	"github.com/tkdtaylor/agent-builder/internal/cli"
)

func main() {
	os.Exit(cli.Main(cli.Config{
		Args:   os.Args[1:],
		Stdout: os.Stdout,
		Stderr: os.Stderr,
		Stdin:  os.Stdin,
	}))
}
