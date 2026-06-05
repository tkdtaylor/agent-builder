// Command agent-builder is the entrypoint for the autonomous block-building agent.
// During Phase 0 it only reports status; the orchestration loop is stubbed.
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

func main() {
	fmt.Printf("agent-builder %s — autonomous block builder (scaffold)\n", supervisor.Version)

	err := supervisor.New().Run()
	if errors.Is(err, supervisor.ErrNotImplemented) {
		fmt.Println("loop not yet implemented — see docs/plans/roadmap.md (Phase 0)")
		return
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
