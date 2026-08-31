package steps

import (
	"strings"
	"testing"

	"github.com/chadgh/code-caretaker/internal/core"
)

// --- helpers ---------------------------------------------------------------

func cfg(typ string, mods ...func(*core.StepConfig)) core.StepConfig {
	c := core.StepConfig{Type: typ, Name: typ, Enabled: true, Params: map[string]any{}}
	for _, m := range mods {
		m(&c)
	}
	return c
}

func prompt(p string) func(*core.StepConfig) { return func(c *core.StepConfig) { c.Prompt = &p } }
func name(n string) func(*core.StepConfig)   { return func(c *core.StepConfig) { c.Name = n } }
func maxPRs(n int) func(*core.StepConfig)    { return func(c *core.StepConfig) { c.MaxOpenPRs = &n } }
func params(p map[string]any) func(*core.StepConfig) {
	return func(c *core.StepConfig) { c.Params = p }
}

func ctxWith(prs ...map[string]any) *core.CycleContext {
	return &core.CycleContext{
		Config:  core.LoopConfig{Repo: "chadgh/class-cash", RepoRoot: "/repo"},
		OpenPRs: prs,
	}
}

func rollup(checks ...map[string]any) []any {
	out := make([]any, len(checks))
	for i, c := range checks {
		out[i] = c
	}
	return out
}

// mustStep unwraps a (Step, error) constructor result, failing on error.
func mustStep(t *testing.T, build func() (core.Step, error)) core.Step {
	t.Helper()
	s, err := build()
	if err != nil {
		t.Fatalf("build step: %v", err)
	}
	return s
}

var (
	failingCheck   = map[string]any{"name": "Lint, typecheck, test, build", "status": "COMPLETED", "conclusion": "FAILURE", "detailsUrl": "https://github.com/chadgh/class-cash/actions/runs/123"}
	cancelledCheck = map[string]any{"name": "E2E", "status": "COMPLETED", "conclusion": "CANCELLED", "detailsUrl": "u"}
	passingCheck   = map[string]any{"name": "Lint, typecheck, test, build", "status": "COMPLETED", "conclusion": "SUCCESS", "detailsUrl": "u"}
	pendingCheck   = map[string]any{"name": "Lint", "status": "IN_PROGRESS", "conclusion": nil, "detailsUrl": "u"}
)

func failingPRMap() map[string]any {
	return map[string]any{"number": float64(13), "title": "feat: some work", "body": "", "headRefName": "feat/some-work", "mergeable": "MERGEABLE", "statusCheckRollup": rollup(failingCheck)}
}
func passingPRMap() map[string]any {
	return map[string]any{"number": float64(14), "title": "feat: other work", "body": "", "headRefName": "feat/other", "mergeable": "MERGEABLE", "statusCheckRollup": rollup(passingCheck)}
}
func conflictedPRMap() map[string]any {
	return map[string]any{"number": float64(15), "title": "feat: conflicted work", "body": "", "headRefName": "feat/conflicted", "mergeable": "CONFLICTING", "statusCheckRollup": rollup(passingCheck)}
}

// --- failing_prs -----------------------------------------------------------

func TestGetFailingPRsReturnsFailedChecks(t *testing.T) {
	result := getFailingPRs([]map[string]any{failingPRMap()})
	if len(result) != 1 || len(result[0].failedChecks) != 1 || result[0].conflicted {
		t.Errorf("result = %+v", result)
	}
}

func TestGetFailingPRsTreatsCancelledAsFailed(t *testing.T) {
	pr := failingPRMap()
	pr["statusCheckRollup"] = rollup(cancelledCheck)
	if len(getFailingPRs([]map[string]any{pr})) != 1 {
		t.Error("cancelled should count as failing")
	}
}

func TestGetFailingPRsSkipsPassing(t *testing.T) {
	if len(getFailingPRs([]map[string]any{passingPRMap()})) != 0 {
		t.Error("passing PR should be skipped")
	}
}

func TestGetFailingPRsSkipsPending(t *testing.T) {
	pr := failingPRMap()
	pr["statusCheckRollup"] = rollup(pendingCheck)
	if len(getFailingPRs([]map[string]any{pr})) != 0 {
		t.Error("pending check should be skipped")
	}
}

func TestGetFailingPRsConflict(t *testing.T) {
	result := getFailingPRs([]map[string]any{conflictedPRMap()})
	if len(result) != 1 || !result[0].conflicted || len(result[0].failedChecks) != 0 {
		t.Errorf("result = %+v", result)
	}
}

func TestGetFailingPRsIgnoresUnknownMergeability(t *testing.T) {
	pr := passingPRMap()
	pr["mergeable"] = "UNKNOWN"
	if len(getFailingPRs([]map[string]any{pr})) != 0 {
		t.Error("UNKNOWN mergeability should not count as conflict")
	}
}

func TestGetFailingPRsIgnoresMissingMergeable(t *testing.T) {
	pr := passingPRMap()
	delete(pr, "mergeable")
	if len(getFailingPRs([]map[string]any{pr})) != 0 {
		t.Error("missing mergeable should not count")
	}
}

func TestGetFailingPRsReportsBothProblems(t *testing.T) {
	pr := conflictedPRMap()
	pr["statusCheckRollup"] = rollup(failingCheck)
	result := getFailingPRs([]map[string]any{pr})
	if !result[0].conflicted || len(result[0].failedChecks) != 1 {
		t.Errorf("result = %+v", result)
	}
}

func failingStep(t *testing.T, mods ...func(*core.StepConfig)) core.Step {
	return mustStep(t, func() (core.Step, error) { return newFailingPrsStep(cfg("failing_prs", mods...)) })
}

func TestFailingFindWorkNilWhenNothingFailing(t *testing.T) {
	if core.Check(failingStep(t), ctxWith(passingPRMap())) != nil {
		t.Error("expected nil")
	}
}

func TestFailingFindWorkForFailingPR(t *testing.T) {
	f := core.Check(failingStep(t), ctxWith(failingPRMap()))
	if f == nil || !strings.Contains(f.Label, "#13") || !strings.Contains(f.Label, "failing checks") {
		t.Errorf("finding = %+v", f)
	}
}

func TestFailingFindWorkForConflicted(t *testing.T) {
	f := core.Check(failingStep(t), ctxWith(conflictedPRMap()))
	if f == nil || !strings.Contains(f.Label, "#15") || !strings.Contains(f.Label, "merge conflict") {
		t.Errorf("finding = %+v", f)
	}
}

func TestFailingLabelNamesBothProblems(t *testing.T) {
	pr := conflictedPRMap()
	pr["statusCheckRollup"] = rollup(failingCheck)
	f := core.Check(failingStep(t), ctxWith(pr))
	if !strings.Contains(f.Label, "merge conflict and failing checks") {
		t.Errorf("label = %q", f.Label)
	}
}

func TestFailingPromptContainsDetails(t *testing.T) {
	f := core.Check(failingStep(t), ctxWith(failingPRMap()))
	for _, want := range []string{"#13", "feat/some-work", "Lint, typecheck, test, build", "actions/runs/123"} {
		if !strings.Contains(f.Prompt, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}

func TestFailingPicksFirstFailingPR(t *testing.T) {
	f := core.Check(failingStep(t), ctxWith(passingPRMap(), failingPRMap()))
	if !strings.Contains(f.Label, "#13") {
		t.Errorf("label = %q", f.Label)
	}
}

func TestFailingRespectsMaxOpenPRs(t *testing.T) {
	if core.Check(failingStep(t, maxPRs(1)), ctxWith(failingPRMap(), passingPRMap())) != nil {
		t.Error("expected nil over cap")
	}
}

func TestFailingCustomPromptRendered(t *testing.T) {
	s := failingStep(t, prompt("Fix PR {pr_number} on {pr_branch} in {repo}"))
	f := core.Check(s, ctxWith(failingPRMap()))
	if f.Prompt != "Fix PR 13 on feat/some-work in chadgh/class-cash" {
		t.Errorf("prompt = %q", f.Prompt)
	}
}

// --- dependabot ------------------------------------------------------------

const sampleAlertsJSON = `[{"number":1,"state":"open","security_vulnerability":{"package":{"name":"lodash"},"severity":"high","vulnerable_version_range":"< 4.17.21","first_patched_version":{"identifier":"4.17.21"}}}]`

func withRunOutput(t *testing.T, stdout string, code int) {
	t.Helper()
	orig := runOutput
	t.Cleanup(func() { runOutput = orig })
	runOutput = func(name string, args ...string) (string, int) { return stdout, code }
}

func TestGetDependabotAlertsReturnsOpen(t *testing.T) {
	withRunOutput(t, sampleAlertsJSON, 0)
	if len(getDependabotAlerts("chadgh/class-cash")) != 1 {
		t.Error("expected 1 alert")
	}
}

func TestGetDependabotAlertsEmptyOnGhError(t *testing.T) {
	withRunOutput(t, "", 1)
	if getDependabotAlerts("chadgh/class-cash") != nil {
		t.Error("expected nil on gh error")
	}
}

func TestGetDependabotAlertsEmptyOnMalformed(t *testing.T) {
	withRunOutput(t, "not json", 0)
	if getDependabotAlerts("chadgh/class-cash") != nil {
		t.Error("expected nil on malformed json")
	}
}

func TestHasDependabotPRTitle(t *testing.T) {
	pr := map[string]any{"title": "chore: resolve Dependabot security alerts (batch)", "headRefName": "fix/dependabot-batch"}
	if !hasDependabotPR([]map[string]any{pr}) {
		t.Error("expected true")
	}
}

func TestHasDependabotPRBranchPrefix(t *testing.T) {
	pr := map[string]any{"title": "Bump lodash", "headRefName": "dependabot/npm/lodash-4.17.21"}
	if !hasDependabotPR([]map[string]any{pr}) {
		t.Error("expected true")
	}
}

func TestHasDependabotPRFalse(t *testing.T) {
	pr := map[string]any{"title": "feat: new feature", "headRefName": "feat/new-feature"}
	if hasDependabotPR([]map[string]any{pr}) {
		t.Error("expected false")
	}
}

func dependabotStep(t *testing.T, mods ...func(*core.StepConfig)) core.Step {
	return mustStep(t, func() (core.Step, error) { return newDependabotStep(cfg("dependabot_alerts", mods...)) })
}

func TestDependabotFindWorkNilWhenNoAlerts(t *testing.T) {
	withRunOutput(t, "[]", 0)
	if core.Check(dependabotStep(t), ctxWith()) != nil {
		t.Error("expected nil")
	}
}

func TestDependabotFindWorkWithAlerts(t *testing.T) {
	withRunOutput(t, sampleAlertsJSON, 0)
	f := core.Check(dependabotStep(t), ctxWith())
	if f == nil || !strings.Contains(f.Label, "1 Dependabot alert(s)") {
		t.Errorf("finding = %+v", f)
	}
	for _, want := range []string{"lodash", "4.17.21", "main"} {
		if !strings.Contains(f.Prompt, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}

func TestDependabotFallsThroughWhenPRExists(t *testing.T) {
	withRunOutput(t, sampleAlertsJSON, 0)
	pr := map[string]any{"title": "chore: resolve Dependabot security alerts (batch)", "headRefName": "fix/dependabot-batch"}
	if core.Check(dependabotStep(t), ctxWith(pr)) != nil {
		t.Error("expected nil when dependabot PR already open")
	}
}

func TestDependabotCustomPrompt(t *testing.T) {
	withRunOutput(t, sampleAlertsJSON, 0)
	f := core.Check(dependabotStep(t, prompt("{count} alerts in {repo}")), ctxWith())
	if f.Prompt != "1 alerts in chadgh/class-cash" {
		t.Errorf("prompt = %q", f.Prompt)
	}
}

// --- labeled_issues --------------------------------------------------------

const sampleIssueJSON = `[{"number":42,"title":"Add CSV export","body":"Export student ledger as CSV."}]`

func TestHasIssuePRBody(t *testing.T) {
	pr := map[string]any{"title": "feat: add CSV export", "body": "Closes #42\n\nDetails.", "headRefName": "feat/csv-export"}
	if !hasIssuePR(42, []map[string]any{pr}) {
		t.Error("expected true")
	}
}

func TestHasIssuePRTitle(t *testing.T) {
	pr := map[string]any{"title": "fix #42: CSV export bug", "body": "", "headRefName": "fix/csv"}
	if !hasIssuePR(42, []map[string]any{pr}) {
		t.Error("expected true")
	}
}

func TestHasIssuePRBranch(t *testing.T) {
	pr := map[string]any{"title": "fix", "body": "", "headRefName": "feat/issue-42-csv"}
	if !hasIssuePR(42, []map[string]any{pr}) {
		t.Error("expected true")
	}
}

func TestHasIssuePRFalse(t *testing.T) {
	pr := map[string]any{"title": "feat", "body": "", "headRefName": "feat/new"}
	if hasIssuePR(42, []map[string]any{pr}) {
		t.Error("expected false")
	}
}

func labeledStep(t *testing.T, mods ...func(*core.StepConfig)) core.Step {
	return mustStep(t, func() (core.Step, error) { return newLabeledIssuesStep(cfg("labeled_issues", mods...)) })
}

func TestLabeledFindWorkNilWhenNoIssues(t *testing.T) {
	withRunOutput(t, "[]", 0)
	if core.Check(labeledStep(t), ctxWith()) != nil {
		t.Error("expected nil")
	}
}

func TestLabeledFindWorkForUncovered(t *testing.T) {
	withRunOutput(t, sampleIssueJSON, 0)
	f := core.Check(labeledStep(t), ctxWith())
	if f == nil || !strings.Contains(f.Label, "#42") || !strings.Contains(f.Prompt, "CSV export") {
		t.Errorf("finding = %+v", f)
	}
}

func TestLabeledFindWorkNilWhenIssueHasPR(t *testing.T) {
	withRunOutput(t, sampleIssueJSON, 0)
	pr := map[string]any{"title": "feat: add CSV export", "body": "Closes #42", "headRefName": "feat/csv-export"}
	if core.Check(labeledStep(t), ctxWith(pr)) != nil {
		t.Error("expected nil when issue already has PR")
	}
}

func TestLabeledPRCapShortCircuits(t *testing.T) {
	// runOutput would panic the test if called; leave it unset (default gh
	// call) but the cap should short-circuit before it runs.
	orig := runOutput
	called := false
	runOutput = func(n string, a ...string) (string, int) { called = true; return "[]", 0 }
	defer func() { runOutput = orig }()
	pr := map[string]any{"title": "x", "body": "", "headRefName": "y"}
	if core.Check(labeledStep(t, maxPRs(1)), ctxWith(pr, pr)) != nil {
		t.Error("expected nil over cap")
	}
	if called {
		t.Error("issues should not be fetched when over cap")
	}
}

func TestLabeledCustomLabelParamUsed(t *testing.T) {
	var gotArgs []string
	orig := runOutput
	runOutput = func(n string, a ...string) (string, int) { gotArgs = a; return "[]", 0 }
	defer func() { runOutput = orig }()
	core.Check(labeledStep(t, params(map[string]any{"label": "triage-me"})), ctxWith())
	joined := strings.Join(gotArgs, " ")
	if !strings.Contains(joined, "triage-me") {
		t.Errorf("args = %v", gotArgs)
	}
}

// --- command ---------------------------------------------------------------

func commandCfg(mods ...func(*core.StepConfig)) core.StepConfig {
	p := "Clean up in {repo}:\n{output}"
	c := core.StepConfig{Type: "command", Name: "stale-branches", Enabled: true,
		Prompt: &p, Params: map[string]any{"check": "echo hi"}}
	for _, m := range mods {
		m(&c)
	}
	return c
}

func withRunCheck(t *testing.T, output string) {
	t.Helper()
	orig := runCheck
	t.Cleanup(func() { runCheck = orig })
	runCheck = func(command, repoRoot string, timeout int) string { return output }
}

func TestCommandRequiresCheckParam(t *testing.T) {
	_, err := newCommandStep(commandCfg(params(map[string]any{})))
	if err == nil || !strings.Contains(err.Error(), "requires a 'check'") {
		t.Errorf("err = %v", err)
	}
}

func TestCommandRequiresPrompt(t *testing.T) {
	c := commandCfg()
	c.Prompt = nil
	_, err := newCommandStep(c)
	if err == nil || !strings.Contains(err.Error(), "requires a 'prompt'") {
		t.Errorf("err = %v", err)
	}
}

func TestCommandFindWorkNilWhenNoOutput(t *testing.T) {
	withRunCheck(t, "")
	s := mustStep(t, func() (core.Step, error) { return newCommandStep(commandCfg()) })
	if core.Check(s, ctxWith()) != nil {
		t.Error("expected nil")
	}
}

func TestCommandFindWorkWithOutput(t *testing.T) {
	withRunCheck(t, "branch-a\nbranch-b")
	s := mustStep(t, func() (core.Step, error) { return newCommandStep(commandCfg()) })
	f := core.Check(s, ctxWith())
	if f == nil || f.Label != "stale-branches" ||
		f.Prompt != "Clean up in chadgh/class-cash:\nbranch-a\nbranch-b" {
		t.Errorf("finding = %+v", f)
	}
}

func TestCommandModelWhenConfigured(t *testing.T) {
	withRunCheck(t, "branch-a")
	s := mustStep(t, func() (core.Step, error) {
		return newCommandStep(commandCfg(params(map[string]any{"check": "echo hi", "model": "claude-opus-4-8"})))
	})
	if core.Check(s, ctxWith()).Model != "claude-opus-4-8" {
		t.Error("expected model set")
	}
}

func TestCommandNoModelByDefault(t *testing.T) {
	withRunCheck(t, "branch-a")
	s := mustStep(t, func() (core.Step, error) { return newCommandStep(commandCfg()) })
	if core.Check(s, ctxWith()).Model != "" {
		t.Error("expected empty model")
	}
}

func TestCommandModelMustBeNonEmpty(t *testing.T) {
	_, err := newCommandStep(commandCfg(params(map[string]any{"check": "echo hi", "model": ""})))
	if err == nil || !strings.Contains(err.Error(), "model") {
		t.Errorf("err = %v", err)
	}
}

func TestCommandUnknownPlaceholderRaises(t *testing.T) {
	_, err := newCommandStep(commandCfg(prompt("uses {bogus}")))
	if err == nil || !strings.Contains(err.Error(), "unknown placeholder") {
		t.Errorf("err = %v", err)
	}
}

func TestCommandRespectsMaxOpenPRs(t *testing.T) {
	called := false
	orig := runCheck
	runCheck = func(command, repoRoot string, timeout int) string { called = true; return "x" }
	defer func() { runCheck = orig }()
	s := mustStep(t, func() (core.Step, error) { return newCommandStep(commandCfg(maxPRs(0))) })
	if core.Check(s, ctxWith(map[string]any{"number": float64(1)})) != nil {
		t.Error("expected nil over cap")
	}
	if called {
		t.Error("check should not run when over cap")
	}
}

// --- registry --------------------------------------------------------------

func TestRegistryContainsAllTypes(t *testing.T) {
	for _, want := range []string{"failing_prs", "dependabot_alerts", "prod_errors", "labeled_issues", "command"} {
		if _, ok := stepTypes[want]; !ok {
			t.Errorf("registry missing %q", want)
		}
	}
	if len(stepTypes) != 5 {
		t.Errorf("registry size = %d", len(stepTypes))
	}
}

func TestBuildInstantiatesInOrder(t *testing.T) {
	built, err := Build([]core.StepConfig{cfg("prod_errors"), cfg("failing_prs")})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := built[0].(*ProdErrorsStep); !ok {
		t.Errorf("built[0] = %T", built[0])
	}
	if _, ok := built[1].(*FailingPrsStep); !ok {
		t.Errorf("built[1] = %T", built[1])
	}
}

func TestBuildRaisesOnUnknownType(t *testing.T) {
	_, err := Build([]core.StepConfig{cfg("nonsense")})
	if err == nil || !strings.Contains(err.Error(), "Unknown step type 'nonsense'") ||
		!strings.Contains(err.Error(), "failing_prs") {
		t.Errorf("err = %v", err)
	}
}

func TestBuildPropagatesConstructionErrors(t *testing.T) {
	c := cfg("command", params(map[string]any{"check": "echo hi"}))
	_, err := Build([]core.StepConfig{c})
	if err == nil || !strings.Contains(err.Error(), "requires a 'prompt'") {
		t.Errorf("err = %v", err)
	}
}

// keep the name helper referenced.
var _ = name
