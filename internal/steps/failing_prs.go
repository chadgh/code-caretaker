package steps

import (
	"strconv"
	"strings"

	"github.com/chadgh/code-caretaker/internal/core"
	"github.com/chadgh/code-caretaker/internal/gh"
	"github.com/chadgh/code-caretaker/internal/status"
)

// FailingPrsType is the config `type` for the failing-PRs step.
const FailingPrsType = "failing_prs"

var failedConclusions = map[string]bool{"FAILURE": true, "CANCELLED": true}

// GitHub reports "UNKNOWN" while it is still computing mergeability (common
// right after a push), so only an explicit "CONFLICTING" counts as a conflict.
const conflicting = "CONFLICTING"

const failingPrsPrompt = `You are an autonomous agent working on the {repo} GitHub repository.

PR #{pr_number} "{pr_title}" is failing:

{problems}

Steps:
1. Check out the PR branch ({pr_branch}).
2. If the PR has a merge conflict, merge the latest origin/main into the branch
   and resolve the conflicts, preserving the intent of both sides.
3. If CI checks are failing, investigate them — look at the workflow logs via the
   details URLs above — and fix the root cause.
4. Push the fix to the same branch (do not open a new PR).

IMPORTANT: Before modifying code locally, ensure you stash changes that are unrelated. Also check stashed changes that might be related.
`

// FailingPrsStep fixes an open PR that is failing — red CI checks or a merge
// conflict.
type FailingPrsStep struct {
	core.Base
}

func newFailingPrsStep(cfg core.StepConfig) (core.Step, error) {
	base, err := core.NewBase(cfg, failingPrsPrompt,
		[]string{"repo", "pr_number", "pr_title", "pr_branch", "problems"})
	if err != nil {
		return nil, err
	}
	return &FailingPrsStep{Base: base}, nil
}

type failingPR struct {
	pr           map[string]any
	failedChecks []map[string]any
	conflicted   bool
}

func getFailingPRs(openPRs []map[string]any) []failingPR {
	var failing []failingPR
	for _, pr := range openPRs {
		var failed []map[string]any
		for _, c := range gh.Rollup(pr) {
			if gh.String(c, "status") == "COMPLETED" &&
				failedConclusions[gh.String(c, "conclusion")] {
				failed = append(failed, c)
			}
		}
		conflicted := gh.String(pr, "mergeable") == conflicting
		if len(failed) > 0 || conflicted {
			failing = append(failing, failingPR{pr: pr, failedChecks: failed, conflicted: conflicted})
		}
	}
	return failing
}

func describeProblems(f failingPR) string {
	var lines []string
	if f.conflicted {
		lines = append(lines, "- merge conflict: the branch cannot be merged into its "+
			"base branch until the conflicts are resolved")
	}
	for _, c := range f.failedChecks {
		details := gh.String(c, "detailsUrl")
		if details == "" {
			details = "no details url"
		}
		lines = append(lines, "- failing check "+gh.String(c, "name")+": "+details)
	}
	return strings.Join(lines, "\n")
}

func summarizeFailing(f failingPR) string {
	var reasons []string
	if f.conflicted {
		reasons = append(reasons, "merge conflict")
	}
	if len(f.failedChecks) > 0 {
		reasons = append(reasons, "failing checks")
	}
	return strings.Join(reasons, " and ")
}

// FindWork returns a Finding for the first failing PR, or nil.
func (s *FailingPrsStep) FindWork(ctx *core.CycleContext) *core.Finding {
	failing := getFailingPRs(ctx.OpenPRs)
	if len(failing) == 0 {
		return nil
	}
	f := failing[0]
	reason := summarizeFailing(f)
	number := gh.Number(f.pr)
	label := "PR #" + strconv.Itoa(number) + " " + reason
	status.Logf("PR #%d has %s.", number, reason)
	return &core.Finding{
		Label: label,
		Prompt: s.Render(map[string]string{
			"repo":      ctx.Config.Repo,
			"pr_number": strconv.Itoa(number),
			"pr_title":  gh.String(f.pr, "title"),
			"pr_branch": gh.String(f.pr, "headRefName"),
			"problems":  describeProblems(f),
		}),
	}
}
