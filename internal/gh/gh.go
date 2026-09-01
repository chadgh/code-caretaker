// Package gh holds shared GitHub queries built on the gh CLI.
package gh

import (
	"encoding/json"
	"os/exec"
	"time"

	"github.com/chadgh/code-caretaker/internal/status"
)

// GitHub computes PR mergeability lazily and reports "UNKNOWN" while it works.
// It re-computes for every open PR whenever the base branch moves, so a cycle
// that starts just after a push to main sees UNKNOWN and would miss a
// conflicted PR entirely. Re-ask a few times before giving up.
const (
	unknown                  = "UNKNOWN"
	mergeabilityAttempts     = 3
	mergeabilityRetrySeconds = 2.0
)

// sleepFn is the sleep used between mergeability retries; a package variable so
// tests can substitute it.
var sleepFn = time.Sleep

// runOutput runs a command and returns its stdout; a package variable so tests
// can substitute the real subprocess call.
var runOutput = func(name string, args ...string) string {
	out, err := exec.Command(name, args...).Output()
	if err != nil {
		// On failure the caller falls back to empty output.
		if len(out) == 0 {
			return ""
		}
	}
	return string(out)
}

// GetOpenPRs fetches open PRs once per cycle; steps read them from the
// CycleContext.
func GetOpenPRs(repo string) []map[string]any {
	stdout := runOutput("gh", "pr", "list", "--repo", repo, "--state", "open",
		"--json", "number,title,body,headRefName,statusCheckRollup,mergeable")
	if stdout == "" {
		stdout = "[]"
	}
	var prs []map[string]any
	if err := json.Unmarshal([]byte(stdout), &prs); err != nil {
		return []map[string]any{}
	}
	resolved := make([]map[string]any, 0, len(prs))
	for _, pr := range prs {
		resolved = append(resolved, resolveMergeability(repo, pr))
	}
	return resolved
}

// resolveMergeability re-queries a PR whose mergeability GitHub hasn't finished
// computing.
func resolveMergeability(repo string, pr map[string]any) map[string]any {
	for i := 0; i < mergeabilityAttempts-1; i++ {
		if str(pr["mergeable"]) != unknown {
			return pr
		}
		sleepFn(time.Duration(mergeabilityRetrySeconds * float64(time.Second)))
		pr["mergeable"] = GetPRMergeable(repo, prNumber(pr))
	}
	if str(pr["mergeable"]) == unknown {
		status.Logf("PR #%s: GitHub still reports mergeable=UNKNOWN; "+
			"skipping the conflict check this cycle.", prNumberString(pr))
	}
	return pr
}

// GetPRMergeable asks GitHub for one PR's mergeability, forcing it to compute
// the value.
func GetPRMergeable(repo string, number int) string {
	stdout := runOutput("gh", "pr", "view", itoa(number), "--repo", repo,
		"--json", "mergeable")
	if stdout == "" {
		stdout = "{}"
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(stdout), &obj); err != nil {
		return unknown
	}
	if v := str(obj["mergeable"]); v != "" {
		return v
	}
	return unknown
}
