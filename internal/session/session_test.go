package session

import (
	"context"
	"strings"
	"testing"
)

func result(stdout, stderr string, code int) Result {
	return Result{Stdout: stdout, Stderr: stderr, ReturnCode: code}
}

func TestIsTokenExhaustedDetectsUsageLimit(t *testing.T) {
	if !IsTokenExhausted(result("Error: usage limit reached for this period", "", 0)) {
		t.Error("expected exhausted")
	}
}

func TestIsTokenExhaustedDetectsPhraseInStderr(t *testing.T) {
	if !IsTokenExhausted(result("", "quota exceeded", 0)) {
		t.Error("expected exhausted")
	}
}

func TestIsTokenExhaustedFalseForNormalOutput(t *testing.T) {
	if IsTokenExhausted(result("PR #45 created successfully.", "", 0)) {
		t.Error("expected not exhausted")
	}
}

func TestClaudeArgsIncludesPrompt(t *testing.T) {
	args := ClaudeArgs("fix the thing", "")
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--dangerously-skip-permissions") ||
		!strings.Contains(joined, "-p") || !strings.Contains(joined, "fix the thing") {
		t.Errorf("args = %v", args)
	}
	if strings.Contains(joined, "--model") {
		t.Errorf("unexpected --model: %v", args)
	}
}

func TestClaudeArgsAddsModelFlag(t *testing.T) {
	args := ClaudeArgs("do it", "claude-opus-4-8")
	idx := indexOf(args, "--model")
	if idx < 0 || args[idx+1] != "claude-opus-4-8" {
		t.Errorf("args = %v", args)
	}
}

func TestRunClaudeSessionPassesRepoRootAndPrompt(t *testing.T) {
	var gotName, gotDir string
	var gotArgs []string
	orig := runCommand
	defer func() { runCommand = orig }()
	runCommand = func(ctx context.Context, name string, args []string, dir, stdin string) Result {
		gotName, gotArgs, gotDir = name, args, dir
		return result("Done", "", 0)
	}
	RunClaudeSession("fix the thing", "/repo", 1800, "")
	if gotName != "claude" || gotDir != "/repo" {
		t.Errorf("name=%q dir=%q", gotName, gotDir)
	}
	if !contains(gotArgs, "fix the thing") {
		t.Errorf("args = %v", gotArgs)
	}
}

func TestRunClaudeSessionTimeout(t *testing.T) {
	orig := runCommand
	defer func() { runCommand = orig }()
	runCommand = func(ctx context.Context, name string, args []string, dir, stdin string) Result {
		// Simulate the deadline being exceeded.
		<-ctx.Done()
		return result("", "", -1)
	}
	res := RunClaudeSession("x", "/repo", 1, "")
	if res.ReturnCode != -1 || !strings.Contains(strings.ToLower(res.Stdout), "timed out") {
		t.Errorf("res = %+v", res)
	}
}

func TestUsageAvailableFalseWhenFullyUsed(t *testing.T) {
	orig := runCommand
	defer func() { runCommand = orig }()
	runCommand = func(ctx context.Context, name string, args []string, dir, stdin string) Result {
		return result("Current session: 100% used", "", 0)
	}
	if UsageAvailable() {
		t.Error("expected unavailable")
	}
}

func TestUsageAvailableTrueWhenCapacityRemains(t *testing.T) {
	orig := runCommand
	defer func() { runCommand = orig }()
	runCommand = func(ctx context.Context, name string, args []string, dir, stdin string) Result {
		return result("Current session: 12% used", "", 0)
	}
	if !UsageAvailable() {
		t.Error("expected available")
	}
}

func TestHandleSessionReportsTokenExhaustion(t *testing.T) {
	var actions []string
	got := HandleSession("dependabot", result("usage limit reached", "", 0), &actions,
		t.TempDir()+"/events.jsonl")
	if got != OutcomeTokenExhausted {
		t.Errorf("outcome = %v", got)
	}
	if !anyContains(actions, "Token exhaustion") {
		t.Errorf("actions = %v", actions)
	}
}

func TestHandleSessionReportsFailure(t *testing.T) {
	var actions []string
	got := HandleSession("dependabot", result("boom", "", 2), &actions,
		t.TempDir()+"/events.jsonl")
	if got != OutcomeFailed {
		t.Errorf("outcome = %v", got)
	}
	if !anyContains(actions, "failed (exit 2)") {
		t.Errorf("actions = %v", actions)
	}
}

func TestHandleSessionRecordsSuccess(t *testing.T) {
	var actions []string
	got := HandleSession("dependabot", result("PR created", "", 0), &actions,
		t.TempDir()+"/events.jsonl")
	if got != OutcomeOK {
		t.Errorf("outcome = %v", got)
	}
	if !anyContains(actions, "completed") {
		t.Errorf("actions = %v", actions)
	}
}

func indexOf(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}

func contains(s []string, v string) bool { return indexOf(s, v) >= 0 }

func anyContains(s []string, sub string) bool {
	for _, x := range s {
		if strings.Contains(x, sub) {
			return true
		}
	}
	return false
}
