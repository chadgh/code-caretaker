package steps

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/chadgh/code-caretaker/internal/core"
	"github.com/chadgh/code-caretaker/internal/gh"
	"github.com/chadgh/code-caretaker/internal/status"
)

// LabeledIssuesType is the config `type` for the labeled-issues step.
const LabeledIssuesType = "labeled_issues"

const labeledIssuesPrompt = `You are an autonomous agent working on the class-cash GitHub repository ({repo}).

Implement the following GitHub issue and create a PR targeting the main branch.
After creating the PR, post a comment on the issue linking to the PR.

Issue #{issue_number}: {issue_title}

{issue_body}

IMPORTANT: Before modifying code locally, ensure you stash changes that are unrelated. Also check stashed changes that might be related.
`

// LabeledIssuesStep implements an open issue carrying a given label.
type LabeledIssuesStep struct {
	core.Base
	label string
	limit int
}

func newLabeledIssuesStep(cfg core.StepConfig) (core.Step, error) {
	base, err := core.NewBase(cfg, labeledIssuesPrompt,
		[]string{"repo", "issue_number", "issue_title", "issue_body"})
	if err != nil {
		return nil, err
	}
	label := "for-agent"
	if v, ok := cfg.Params["label"].(string); ok {
		label = v
	}
	limit := 50
	if v, ok := paramInt(cfg.Params, "limit"); ok {
		limit = v
	}
	return &LabeledIssuesStep{Base: base, label: label, limit: limit}, nil
}

func getLabeledIssues(repo, label string, limit int) []map[string]any {
	stdout, _ := runOutput("gh", "issue", "list", "--repo", repo, "--label", label,
		"--state", "open", "--json", "number,title,body", "--limit", strconv.Itoa(limit))
	if strings.TrimSpace(stdout) == "" {
		stdout = "[]"
	}
	var issues []map[string]any
	if err := json.Unmarshal([]byte(stdout), &issues); err != nil {
		return nil
	}
	return issues
}

func hasIssuePR(issueNumber int, openPRs []map[string]any) bool {
	ref := "#" + strconv.Itoa(issueNumber)
	num := strconv.Itoa(issueNumber)
	for _, pr := range openPRs {
		if strings.Contains(gh.String(pr, "body"), ref) ||
			strings.Contains(gh.String(pr, "title"), ref) ||
			strings.Contains(gh.String(pr, "headRefName"), num) {
			return true
		}
	}
	return false
}

// FindWork returns a Finding for the first labeled issue without an open PR,
// else nil.
func (s *LabeledIssuesStep) FindWork(ctx *core.CycleContext) *core.Finding {
	issues := getLabeledIssues(ctx.Config.Repo, s.label, s.limit)
	if len(issues) == 0 {
		return nil
	}
	var issue map[string]any
	for _, i := range issues {
		if !hasIssuePR(gh.Number(i), ctx.OpenPRs) {
			issue = i
			break
		}
	}
	if issue == nil {
		status.Logf("All %d '%s' issue(s) already have open PRs.", len(issues), s.label)
		return nil
	}
	number := gh.Number(issue)
	title := gh.String(issue, "title")
	label := "issue #" + strconv.Itoa(number) + ": " + title
	status.Logf("Found %s %s. Dispatching claude session...", s.label, label)
	return &core.Finding{
		Label: label,
		Prompt: s.Render(map[string]string{
			"repo":         ctx.Config.Repo,
			"issue_number": strconv.Itoa(number),
			"issue_title":  title,
			"issue_body":   gh.String(issue, "body"),
		}),
	}
}
