package loop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	sleeps      []time.Duration
	prCalls     int
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
	origClaude, origHandle, origSleep := runClaudeSession, handleSession, sleep

	resetToMain = func(root string) { e.resetArg = root }
	usageAvailable = func() bool { return usage }
	getOpenPRs = func(repo string) []map[string]any { e.prCalls++; return openPRs }
	runClaudeSession = func(prompt, root string, timeout int, model string) session.Result {
		e.claudeCalls = append(e.claudeCalls, claudeCall{prompt, root, timeout, model})
		return claudeResult
	}
	handleSession = func(label string, r session.Result, actions *[]string, sf string, ts int) bool {
		return session.HandleSession(label, r, actions, sf, ts)
	}
	sleep = func(d time.Duration) { e.sleeps = append(e.sleeps, d) }

	// Avoid a real 3600s pause inside HandleSession on token exhaustion.
	origSessionSleep := session.Sleep
	session.Sleep = func(d time.Duration) { e.sleeps = append(e.sleeps, d) }

	e.restore = func() {
		resetToMain, usageAvailable, getOpenPRs = origReset, origUsage, origPRs
		runClaudeSession, handleSession, sleep = origClaude, origHandle, origSleep
		session.Sleep = origSessionSleep
	}
	return e
}

func okResult() session.Result {
	return session.Result{Stdout: "PR created", ReturnCode: 0}
}
func exhaustedResult() session.Result {
	return session.Result{Stdout: "usage limit reached", ReturnCode: 0}
}

func TestDispatchesFirstStepWithWork(t *testing.T) {
	e := setup(t, nil, okResult(), true)
	defer e.restore()
	first := &fakeStep{name: "first", finding: &core.Finding{Label: "work", Prompt: "do it"}}
	second := &fakeStep{name: "second", finding: &core.Finding{Label: "other", Prompt: "other"}}
	RunOneCycle([]core.Step{first, second}, testConfig(t), &[]string{})
	if len(e.claudeCalls) != 1 || e.claudeCalls[0].prompt != "do it" {
		t.Errorf("claude calls = %+v", e.claudeCalls)
	}
}

func TestLaterStepsNotCheckedAfterMatch(t *testing.T) {
	e := setup(t, nil, okResult(), true)
	defer e.restore()
	first := &fakeStep{name: "first", finding: &core.Finding{Label: "work", Prompt: "do it"}}
	second := &fakeStep{name: "second", finding: &core.Finding{Label: "other", Prompt: "other"}}
	RunOneCycle([]core.Step{first, second}, testConfig(t), &[]string{})
	if !first.called || second.called {
		t.Errorf("first.called=%v second.called=%v", first.called, second.called)
	}
}

func TestFallsThroughStepsReturningNil(t *testing.T) {
	e := setup(t, nil, okResult(), true)
	defer e.restore()
	first := &fakeStep{name: "first", finding: nil}
	second := &fakeStep{name: "second", finding: &core.Finding{Label: "work", Prompt: "second's work"}}
	RunOneCycle([]core.Step{first, second}, testConfig(t), &[]string{})
	if !second.called || e.claudeCalls[0].prompt != "second's work" {
		t.Errorf("second.called=%v calls=%+v", second.called, e.claudeCalls)
	}
}

func TestNoDispatchWhenNoStepHasWork(t *testing.T) {
	e := setup(t, nil, okResult(), true)
	defer e.restore()
	var actions []string
	shouldSleep := RunOneCycle([]core.Step{&fakeStep{name: "a"}}, testConfig(t), &actions)
	if len(e.claudeCalls) != 0 {
		t.Errorf("unexpected claude calls: %+v", e.claudeCalls)
	}
	if !shouldSleep {
		t.Error("expected shouldSleep true")
	}
	if !anyContains(actions, "Nothing to do") {
		t.Errorf("actions = %v", actions)
	}
}

func TestOpenPRsFetchedOncePerCycle(t *testing.T) {
	e := setup(t, nil, okResult(), true)
	defer e.restore()
	steps := []core.Step{&fakeStep{name: "a"}, &fakeStep{name: "b"}}
	RunOneCycle(steps, testConfig(t), &[]string{})
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
	RunOneCycle([]core.Step{&fakeStep{name: "prod", finding: finding}}, cfg, &[]string{})
	data, _ := os.ReadFile(cfg.StatusFile)
	if !strings.Contains(string(data), "prod_errors") {
		t.Errorf("status feed = %s", data)
	}
}

func TestTokenExhaustionReportsNoFurtherSleep(t *testing.T) {
	e := setup(t, nil, exhaustedResult(), true)
	defer e.restore()
	finding := &core.Finding{Label: "work", Prompt: "do it"}
	shouldSleep := RunOneCycle([]core.Step{&fakeStep{name: "a", finding: finding}}, testConfig(t), &[]string{})
	if shouldSleep {
		t.Error("expected shouldSleep false")
	}
}

func TestSuccessfulDispatchReportsSleep(t *testing.T) {
	e := setup(t, nil, okResult(), true)
	defer e.restore()
	finding := &core.Finding{Label: "work", Prompt: "do it"}
	shouldSleep := RunOneCycle([]core.Step{&fakeStep{name: "a", finding: finding}}, testConfig(t), &[]string{})
	if !shouldSleep {
		t.Error("expected shouldSleep true")
	}
}

func TestUsageLimitSkipsCycleAndSleepsTokenSleep(t *testing.T) {
	e := setup(t, nil, okResult(), false)
	defer e.restore()
	step := &fakeStep{name: "a", finding: &core.Finding{Label: "work", Prompt: "do it"}}
	shouldSleep := RunOneCycle([]core.Step{step}, testConfig(t), &[]string{})
	if shouldSleep {
		t.Error("expected shouldSleep false")
	}
	if step.called {
		t.Error("step should not be checked")
	}
	if len(e.claudeCalls) != 0 {
		t.Errorf("claude called: %+v", e.claudeCalls)
	}
	if len(e.sleeps) != 1 || e.sleeps[0] != 3600*time.Second {
		t.Errorf("sleeps = %v", e.sleeps)
	}
}

func TestCycleResetsToMainFirst(t *testing.T) {
	e := setup(t, nil, okResult(), true)
	defer e.restore()
	RunOneCycle([]core.Step{&fakeStep{name: "a"}}, testConfig(t), &[]string{})
	if e.resetArg != "/repo" {
		t.Errorf("resetArg = %q", e.resetArg)
	}
}

func TestClaudeSessionGetsRepoRootAndTimeout(t *testing.T) {
	e := setup(t, nil, okResult(), true)
	defer e.restore()
	finding := &core.Finding{Label: "work", Prompt: "do it"}
	RunOneCycle([]core.Step{&fakeStep{name: "a", finding: finding}}, testConfig(t), &[]string{})
	if e.claudeCalls[0].repoRoot != "/repo" || e.claudeCalls[0].timeout != 1800 {
		t.Errorf("call = %+v", e.claudeCalls[0])
	}
}

func TestClaudeSessionGetsModelFromFinding(t *testing.T) {
	e := setup(t, nil, okResult(), true)
	defer e.restore()
	finding := &core.Finding{Label: "work", Prompt: "do it", Model: "claude-opus-4-8"}
	RunOneCycle([]core.Step{&fakeStep{name: "a", finding: finding}}, testConfig(t), &[]string{})
	if e.claudeCalls[0].model != "claude-opus-4-8" {
		t.Errorf("model = %q", e.claudeCalls[0].model)
	}
}

func TestClaudeSessionModelEmptyWhenFindingOmitsIt(t *testing.T) {
	e := setup(t, nil, okResult(), true)
	defer e.restore()
	finding := &core.Finding{Label: "work", Prompt: "do it"}
	RunOneCycle([]core.Step{&fakeStep{name: "a", finding: finding}}, testConfig(t), &[]string{})
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
