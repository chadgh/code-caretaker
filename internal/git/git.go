// Package git holds git helpers for the agent loop.
package git

import "os/exec"

// ResetToMain returns the working tree to a clean main before each cycle.
func ResetToMain(repoRoot string) {
	run(repoRoot, "git", "checkout", "main")
	run(repoRoot, "git", "pull")
}

func run(dir string, name string, args ...string) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	// Best-effort: output and errors are intentionally ignored, matching the
	// original's check=False, capture_output=True.
	_ = cmd.Run()
}
