package steps

import (
	"encoding/json"
	"os/exec"
	"strconv"
	"strings"

	"github.com/chadgh/code-caretaker/internal/core"
	"github.com/chadgh/code-caretaker/internal/gh"
	"github.com/chadgh/code-caretaker/internal/status"
)

// DependabotType is the config `type` for the Dependabot step.
const DependabotType = "dependabot_alerts"

const dependabotPrompt = `You are an autonomous agent working on the class-cash GitHub repository ({repo}).

There are {count} open Dependabot security alert(s). Resolve all of them in a single batch PR.

Alerts:
{alerts}

Steps:
1. Update the affected packages across all workspaces (apps/api, apps/web, packages/types) to their patched versions.
2. Run ` + "`npm install`" + ` from the repo root to update package-lock.json.
3. Run ` + "`npm run typecheck && npm run test`" + ` to confirm nothing breaks.
4. Create a PR targeting the main branch titled "chore: resolve Dependabot security alerts (batch)".

IMPORTANT: Before modifying code locally, ensure you stash changes that are unrelated. Also check stashed changes that might be related.
`

// runOutput runs a command and returns stdout; a package variable so tests can
// substitute the real subprocess call.
var runOutput = func(name string, args ...string) (string, int) {
	out, err := exec.Command(name, args...).Output()
	code := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		} else {
			code = -1
		}
	}
	return string(out), code
}

// DependabotStep batch-resolves open Dependabot security alerts.
type DependabotStep struct {
	core.Base
}

func newDependabotStep(cfg core.StepConfig) (core.Step, error) {
	base, err := core.NewBase(cfg, dependabotPrompt,
		[]string{"repo", "count", "alerts"})
	if err != nil {
		return nil, err
	}
	return &DependabotStep{Base: base}, nil
}

func getDependabotAlerts(repo string) []map[string]any {
	stdout, code := runOutput("gh", "api", "/repos/"+repo+"/dependabot/alerts",
		"--jq", `[.[] | select(.state == "open")]`)
	if code != 0 || strings.TrimSpace(stdout) == "" {
		return nil
	}
	var alerts []map[string]any
	if err := json.Unmarshal([]byte(stdout), &alerts); err != nil {
		return nil
	}
	return alerts
}

func hasDependabotPR(openPRs []map[string]any) bool {
	for _, pr := range openPRs {
		if strings.Contains(strings.ToLower(gh.String(pr, "title")), "dependabot") ||
			strings.HasPrefix(gh.String(pr, "headRefName"), "dependabot/") {
			return true
		}
	}
	return false
}

func formatAlerts(alerts []map[string]any) string {
	lines := make([]string, 0, len(alerts))
	for _, a := range alerts {
		vuln, _ := a["security_vulnerability"].(map[string]any)
		pkg, _ := vuln["package"].(map[string]any)
		fix := "unknown"
		if fp, ok := vuln["first_patched_version"].(map[string]any); ok {
			if id, ok := fp["identifier"].(string); ok {
				fix = id
			}
		}
		lines = append(lines, "- #"+numToString(a["number"])+": "+
			str(pkg["name"])+" ("+str(vuln["severity"])+") — "+
			"vulnerable: "+str(vuln["vulnerable_version_range"])+", "+
			"fix: "+fix)
	}
	return strings.Join(lines, "\n")
}

// FindWork returns a Finding when there are unresolved Dependabot alerts with
// no existing PR, else nil.
func (s *DependabotStep) FindWork(ctx *core.CycleContext) *core.Finding {
	alerts := getDependabotAlerts(ctx.Config.Repo)
	if len(alerts) == 0 {
		return nil
	}
	if hasDependabotPR(ctx.OpenPRs) {
		status.Logf("%d Dependabot alert(s) open, but a PR already exists — "+
			"falling through.", len(alerts))
		return nil
	}
	label := strconv.Itoa(len(alerts)) + " Dependabot alert(s)"
	status.Logf("Found %s. Dispatching claude session...", label)
	return &core.Finding{
		Label: label,
		Prompt: s.Render(map[string]string{
			"repo":   ctx.Config.Repo,
			"count":  strconv.Itoa(len(alerts)),
			"alerts": formatAlerts(alerts),
		}),
	}
}
