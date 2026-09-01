// Command code-caretaker is an autonomous agent loop for a GitHub repository.
// It wakes up on an interval, walks a list of prioritized steps, and
// dispatches a Claude Code session for the first step that finds work.
//
// Steps come from agent_loop.toml (see internal/config for resolution order).
// Run "code-caretaker help" for the commands and flags.
package main

import (
	"os"

	"github.com/chadgh/code-caretaker/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:]))
}
