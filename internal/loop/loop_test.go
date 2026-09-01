package loop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chadgh/code-caretaker/internal/core"
	"github.com/chadgh/code-caretaker/internal/session"
	"github.com/chadgh/code-caretaker/internal/status"
)

// fakeStep records whether it was checked and returns a canned Finding.
type fakeStep struct {
	name    string
	finding *core.Finding
	called  bool
}

func (f *fakeStep) Name() string    { return f.name }
func (f *fakeStep) MaxOpenPRs() int { return -1 }
func (f *fakeStep) FindWork(ctx *core.CycleContext) *core.Finding {
	f.called = true
	return f.finding
}

func testConfig(t *testing.T) core.LoopConfig {
	return core.LoopConfig{
		Repo:                 "owner/repo",
		CheckIntervalSeconds: 300,
		TokenSleepSeconds:    3600,
		ClaudeTimeoutSeconds: 1800,
		StatusFile:           filepath.Join(t.TempDir(), "events.jsonl"),
		RepoRoot:             "/repo",
	}
}

// env holds the overridable collaborators for one test and restores them.
type env struct {
	claudeCalls []claudeCall
	prCalls     int
	resetCalls  int
	resetArg    string
	restore     func()
}

type claudeCall struct {
	prompt   string
	repoRoot string
	timeout  int
	model    string
}

func setup(t *testing.T, openPRs []map[string]any, claudeResult session.Result, usage bool) *env {
	t.Helper()
	e := &env{}
	origReset, origUsage, origPRs := resetToMain, usageAvailable, getOpenPRs
	origClaude, origHandle := runClaudeSession, handleSession

	resetToMain = func(root string) { e.resetCalls++; e.resetArg = root }
	usageAvailable = func() bool { return usage }
	getOpenPRs = func(repo string) []map[string]any { e.prCalls++; return openPRs }
	runClaudeSession = func(prompt, root string, timeout int, model string) session.Result {
		e.claudeCalls = append(e.claudeCalls, claudeCall{prompt, root, timeout, model})
		return claudeResult
	}
	handleSession = session.HandleSession

	e.restore = func() {
		resetToMain, usageAvailable, getOpenPRs = origReset, origUsage, origPRs
		runClaudeSession, handleSession = origClaude, origHandle
	}
	return e
}

func okResult() session.Result {
	return session.Result{Stdout: "PR created", ReturnCode: 0}
}
func exhaustedResult() session.Result {
	return session.Result{Stdout: "usage limit reached", ReturnCode: 0}
}

// once runs a pass with the default options and discards the action log.
func once(steps []core.Step, cfg core.LoopConfig) Result {
	return RunOnce(steps, cfg, Options{}, &[]string{})
}

func TestDispatchesFirstStepWithWork(t *testing.T) {
	e := setup(t, nil, okResult(), true)
	defer e.restore()
	first := &fakeStep{name: "first", finding: &core.Finding{Label: "work", Prompt: "do it"}}
	second := &fakeStep{name: "second", finding: &core.Finding{Label: "other", Prompt: "other"}}
	once([]core.Step{first, second}, testConfig(t))
	if len(e.claudeCalls) != 1 || e.claudeCalls[0].prompt != "do it" {
		t.Errorf("claude calls = %+v", e.claudeCalls)
	}
}

func TestLaterStepsNotCheckedAfterMatch(t *testing.T) {
	e := setup(t, nil, okResult(), true)
	defer e.restore()
	first := &fakeStep{name: "first", finding: &core.Finding{Label: "work", Prompt: "do it"}}
	second := &fakeStep{name: "second", finding: &core.Finding{Label: "other", Prompt: "other"}}
	once([]core.Step{first, second}, testConfig(t))
	if !first.called || second.called {
		t.Errorf("first.called=%v second.called=%v", first.called, second.called)
	}
}

func TestFallsThroughStepsReturningNil(t *testing.T) {
	e := setup(t, nil, okResult(), true)
	defer e.restore()
	first := &fakeStep{name: "first", finding: nil}
	second := &fakeStep{name: "second", finding: &core.Finding{Label: "work", Prompt: "second's work"}}
	once([]core.Step{first, second}, testConfig(t))
	if !second.called || e.claudeCalls[0].prompt != "second's work" {
		t.Errorf("second.called=%v calls=%+v", second.called, e.claudeCalls)
	}
}

func TestNoDispatchWhenNoStepHasWork(t *testing.T) {
	e := setup(t, nil, okResult(), true)
	defer e.restore()
	var actions []string
	res := RunOnce([]core.Step{&fakeStep{name: "a"}}, testConfig(t), Options{}, &actions)
	if len(e.claudeCalls) != 0 {
		t.Errorf("unexpected claude calls: %+v", e.claudeCalls)
	}
	if res.FoundWork() || res.Backoff() {
		t.Errorf("result = %+v", res)
	}
	if !anyContains(actions, "Nothing to do") {
		t.Errorf("actions = %v", actions)
	}
}

func TestResultNamesTheStepThatFoundWork(t *testing.T) {
	e := setup(t, nil, okResult(), true)
	defer e.restore()
	finding := &core.Finding{Label: "issue #4", Prompt: "do it"}
	res := once([]core.Step{&fakeStep{name: "labeled_issues", finding: finding}}, testConfig(t))
	if res.Step != "labeled_issues" || res.Label != "issue #4" || res.Prompt != "do it" {
		t.Errorf("result = %+v", res)
	}
	if !res.Dispatched || res.Outcome != session.OutcomeOK {
		t.Errorf("result = %+v", res)
	}
}

func TestOpenPRsFetchedOncePerCycle(t *testing.T) {
	e := setup(t, nil, okResult(), true)
	defer e.restore()
	steps := []core.Step{&fakeStep{name: "a"}, &fakeStep{name: "b"}}
	once(steps, testConfig(t))
	if e.prCalls != 1 {
		t.Errorf("prCalls = %d", e.prCalls)
	}
}

func TestPreDispatchEventsEmittedBeforeSession(t *testing.T) {
	e := setup(t, nil, okResult(), true)
	defer e.restore()
	cfg := testConfig(t)
	finding := &core.Finding{Label: "prod", Prompt: "fix", PreDispatchEvents: []status.Event{{
		Event: "prod_errors", Level: "error", Notify: true, Summary: "boom",
		Fields: map[string]any{"logs": "x"},
	}}}
	once([]core.Step{&fakeStep{name: "prod", finding: finding}}, cfg)
	data, _ := os.ReadFile(cfg.StatusFile)
	if !strings.Contains(string(data), "prod_errors") {
		t.Errorf("status feed = %s", data)
	}
}

func TestTokenExhaustionAsksForBackoff(t *testing.T) {
	e := setup(t, nil, exhaustedResult(), true)
	defer e.restore()
	finding := &core.Finding{Label: "work", Prompt: "do it"}
	res := once([]core.Step{&fakeStep{name: "a", finding: finding}}, testConfig(t))
	if !res.Backoff() || res.Outcome != session.OutcomeTokenExhausted {
		t.Errorf("result = %+v", res)
	}
}

func TestSuccessfulDispatchDoesNotAskForBackoff(t *testing.T) {
	e := setup(t, nil, okResult(), true)
	defer e.restore()
	finding := &core.Finding{Label: "work", Prompt: "do it"}
	res := once([]core.Step{&fakeStep{name: "a", finding: finding}}, testConfig(t))
	if res.Backoff() {
		t.Errorf("result = %+v", res)
	}
}

func TestUsageLimitSkipsStepsAndAsksForBackoff(t *testing.T) {
	e := setup(t, nil, okResult(), false)
	defer e.restore()
	step := &fakeStep{name: "a", finding: &core.Finding{Label: "work", Prompt: "do it"}}
	res := once([]core.Step{step}, testConfig(t))
	if !res.UsageBlocked || !res.Backoff() {
		t.Errorf("result = %+v", res)
	}
	if step.called {
		t.Error("step should not be checked")
	}
	if len(e.claudeCalls) != 0 {
		t.Errorf("claude called: %+v", e.claudeCalls)
	}
}

func TestCycleResetsToMainFirst(t *testing.T) {
	e := setup(t, nil, okResult(), true)
	defer e.restore()
	once([]core.Step{&fakeStep{name: "a"}}, testConfig(t))
	if e.resetArg != "/repo" {
		t.Errorf("resetArg = %q", e.resetArg)
	}
}

func TestSkipResetLeavesTheWorkingTreeAlone(t *testing.T) {
	e := setup(t, nil, okResult(), true)
	defer e.restore()
	RunOnce([]core.Step{&fakeStep{name: "a"}}, testConfig(t),
		Options{SkipReset: true}, &[]string{})
	if e.resetCalls != 0 {
		t.Errorf("resetCalls = %d", e.resetCalls)
	}
}

func TestDryRunReportsWorkWithoutDispatching(t *testing.T) {
	// usage=false: a dry run must not need Claude usage to report its prompt.
	e := setup(t, nil, okResult(), false)
	defer e.restore()
	finding := &core.Finding{Label: "work", Prompt: "the rendered prompt"}
	res := RunOnce([]core.Step{&fakeStep{name: "a", finding: finding}}, testConfig(t),
		Options{DryRun: true}, &[]string{})
	if len(e.claudeCalls) != 0 {
		t.Errorf("claude called: %+v", e.claudeCalls)
	}
	if !res.FoundWork() || res.Dispatched || res.Prompt != "the rendered prompt" {
		t.Errorf("result = %+v", res)
	}
	if e.resetCalls != 0 {
		t.Errorf("a dry run reset the working tree (%d calls)", e.resetCalls)
	}
}

func TestClaudeSessionGetsRepoRootAndTimeout(t *testing.T) {
	e := setup(t, nil, okResult(), true)
	defer e.restore()
	finding := &core.Finding{Label: "work", Prompt: "do it"}
	once([]core.Step{&fakeStep{name: "a", finding: finding}}, testConfig(t))
	if e.claudeCalls[0].repoRoot != "/repo" || e.claudeCalls[0].timeout != 1800 {
		t.Errorf("call = %+v", e.claudeCalls[0])
	}
}

func TestClaudeSessionGetsModelFromFinding(t *testing.T) {
	e := setup(t, nil, okResult(), true)
	defer e.restore()
	finding := &core.Finding{Label: "work", Prompt: "do it", Model: "claude-opus-4-8"}
	once([]core.Step{&fakeStep{name: "a", finding: finding}}, testConfig(t))
	if e.claudeCalls[0].model != "claude-opus-4-8" {
		t.Errorf("model = %q", e.claudeCalls[0].model)
	}
}

func TestClaudeSessionModelEmptyWhenFindingOmitsIt(t *testing.T) {
	e := setup(t, nil, okResult(), true)
	defer e.restore()
	finding := &core.Finding{Label: "work", Prompt: "do it"}
	once([]core.Step{&fakeStep{name: "a", finding: finding}}, testConfig(t))
	if e.claudeCalls[0].model != "" {
		t.Errorf("model = %q", e.claudeCalls[0].model)
	}
}

func anyContains(s []string, sub string) bool {
	for _, x := range s {
		if strings.Contains(x, sub) {
			return true
		}
	}
	return false
}
