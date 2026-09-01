// Package session dispatches Claude sessions and interprets their outcomes.
package session

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/chadgh/code-caretaker/internal/status"
)

// tokenExhaustionPhrases mark a session that ran out of usage/quota.
var tokenExhaustionPhrases = []string{
	"usage limit",
	"rate limit",
	"out of tokens",
	"too many requests",
	"quota exceeded",
	"credit balance",
	"monthly limit",
	"overloaded",
}

// Result is the outcome of a finished session, mirroring the fields the loop
// reads from a subprocess result.
type Result struct {
	Stdout     string
	Stderr     string
	ReturnCode int
}

// Outcome classifies how a dispatched session ended. Callers decide what to do
// about it — HandleSession reports, it never sleeps.
type Outcome int

const (
	// OutcomeOK is a session that finished successfully.
	OutcomeOK Outcome = iota
	// OutcomeFailed is a session that exited non-zero.
	OutcomeFailed
	// OutcomeTokenExhausted is a session that ran out of Claude usage. The
	// caller should back off rather than dispatch again immediately.
	OutcomeTokenExhausted
)

// runCommand executes a command and returns its combined result. It is a
// package variable so tests can substitute the real subprocess call.
var runCommand = func(ctx context.Context, name string, args []string, dir, stdin string) Result {
	cmd := exec.CommandContext(ctx, name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var out, errBuf strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	err := cmd.Run()
	code := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		} else {
			code = -1
		}
	}
	return Result{Stdout: out.String(), Stderr: errBuf.String(), ReturnCode: code}
}

// IsTokenExhausted reports whether a session's output signals token/usage
// exhaustion.
func IsTokenExhausted(result Result) bool {
	combined := strings.ToLower(result.Stdout + result.Stderr)
	for _, phrase := range tokenExhaustionPhrases {
		if strings.Contains(combined, phrase) {
			return true
		}
	}
	return false
}

// ClaudeArgs builds the claude CLI arguments for a session.
func ClaudeArgs(prompt, model string) []string {
	args := []string{"--dangerously-skip-permissions"}
	if model != "" {
		args = append(args, "--model", model)
	}
	args = append(args, "-p", prompt)
	return args
}

// RunClaudeSession dispatches a Claude session. On timeout it returns a result
// with ReturnCode -1 and a timeout message, matching the original behavior.
func RunClaudeSession(prompt, repoRoot string, timeout int, model string) Result {
	ctx, cancel := context.WithTimeout(context.Background(),
		time.Duration(timeout)*time.Second)
	defer cancel()

	result := runCommand(ctx, "claude", ClaudeArgs(prompt, model), repoRoot, "")
	if ctx.Err() == context.DeadlineExceeded {
		return Result{
			ReturnCode: -1,
			Stdout:     fmt.Sprintf("Session timed out after %ds.", timeout),
		}
	}
	return result
}

// UsageAvailable checks whether Claude usage remains (best-effort, never
// fatal).
func UsageAvailable() bool {
	result := runCommand(context.Background(), "claude", []string{"-p"}, "", "/usage")
	return !strings.Contains(result.Stdout, "Current session: 100% used")
}

// HandleSession inspects a finished session, emitting notify events for bad
// outcomes and appending to actions. It reports how the session ended; backing
// off after OutcomeTokenExhausted is the caller's job.
func HandleSession(label string, result Result, actions *[]string, statusFile string) Outcome {
	if IsTokenExhausted(result) {
		msg := fmt.Sprintf("Token exhaustion during %s.", label)
		*actions = append(*actions, msg)
		status.Log(msg)
		status.Emit(statusFile, status.Event{
			Event: "token_exhausted", Level: "warn", Notify: true, Summary: msg,
			Fields: map[string]any{"label": label},
		})
		return OutcomeTokenExhausted
	}

	if result.ReturnCode != 0 {
		msg := fmt.Sprintf("Claude session for %s failed (exit %d).", label, result.ReturnCode)
		*actions = append(*actions, msg)
		status.Log(msg)
		status.Emit(statusFile, status.Event{
			Event: "session_failed", Level: "error", Notify: true, Summary: msg,
			Fields: map[string]any{
				"label":       label,
				"returncode":  result.ReturnCode,
				"output_tail": tail(result.Stdout, 500),
			},
		})
		return OutcomeFailed
	}

	*actions = append(*actions, fmt.Sprintf("Dispatched and completed Claude session for %s.", label))
	return OutcomeOK
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
