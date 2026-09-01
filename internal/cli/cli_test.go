package cli

import (
	"flag"
	"io"
	"strings"
	"testing"

	"github.com/chadgh/code-caretaker/internal/core"
	"github.com/chadgh/code-caretaker/internal/loop"
	"github.com/chadgh/code-caretaker/internal/session"
)

func TestBareFlagsMeanRunTheLoop(t *testing.T) {
	cmd, rest := splitCommand([]string{"--config", "other.toml"})
	if cmd != "run" || strings.Join(rest, " ") != "--config other.toml" {
		t.Errorf("cmd=%q rest=%v", cmd, rest)
	}
}

func TestNoArgumentsMeansRunTheLoop(t *testing.T) {
	cmd, rest := splitCommand(nil)
	if cmd != "run" || len(rest) != 0 {
		t.Errorf("cmd=%q rest=%v", cmd, rest)
	}
}

func TestLeadingWordIsTheSubcommand(t *testing.T) {
	cmd, rest := splitCommand([]string{"step", "failing_prs", "--dry-run"})
	if cmd != "step" || strings.Join(rest, " ") != "failing_prs --dry-run" {
		t.Errorf("cmd=%q rest=%v", cmd, rest)
	}
}

func configured() []core.StepConfig {
	return []core.StepConfig{
		{Type: "failing_prs", Name: "failing_prs", Enabled: true},
		{Type: "command", Name: "refine-issues", Enabled: false},
	}
}

func TestFindStepByName(t *testing.T) {
	got, err := findStep(configured(), "refine-issues")
	if err != nil || got.Type != "command" {
		t.Errorf("got = %+v, err = %v", got, err)
	}
}

// A disabled step is still runnable when it is named explicitly; that is the
// point of naming it.
func TestFindStepReturnsDisabledSteps(t *testing.T) {
	got, err := findStep(configured(), "refine-issues")
	if err != nil || got.Enabled {
		t.Errorf("got = %+v, err = %v", got, err)
	}
}

func TestFindStepFallsBackToType(t *testing.T) {
	steps := []core.StepConfig{{Type: "failing_prs", Name: "fix-red-prs", Enabled: true}}
	got, err := findStep(steps, "failing_prs")
	if err != nil || got.Name != "fix-red-prs" {
		t.Errorf("got = %+v, err = %v", got, err)
	}
}

func TestFindStepPrefersNameOverType(t *testing.T) {
	steps := []core.StepConfig{
		{Type: "command", Name: "failing_prs", Enabled: true},
		{Type: "failing_prs", Name: "fix-red-prs", Enabled: true},
	}
	got, err := findStep(steps, "failing_prs")
	if err != nil || got.Type != "command" {
		t.Errorf("got = %+v, err = %v", got, err)
	}
}

func TestFindStepUnknownNameListsWhatIsAvailable(t *testing.T) {
	_, err := findStep(configured(), "nope")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "failing_prs") ||
		!strings.Contains(err.Error(), "refine-issues") {
		t.Errorf("err = %v", err)
	}
}

// inlineFlags parses a `step` command line the way runStep does, returning the
// step config it would build.
func inlineFlags(t *testing.T, args ...string) core.StepConfig {
	t.Helper()
	fs := flag.NewFlagSet("step", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	stepType := fs.String("type", "", "")
	stepName := fs.String("name", "", "")
	prompt := fs.String("prompt", "", "")
	maxOpenPRs := fs.Int("max-open-prs", -1, "")
	params := paramList{}
	fs.Var(params, "param", "")
	if err := fs.Parse(args); err != nil {
		t.Fatalf("parse: %v", err)
	}
	return inlineStep(*stepType, *stepName, *prompt, params, maxOpenPRs, fs)
}

func TestInlineStepNameDefaultsToType(t *testing.T) {
	got := inlineFlags(t, "--type", "failing_prs")
	if got.Name != "failing_prs" || !got.Enabled {
		t.Errorf("got = %+v", got)
	}
}

func TestInlineStepCollectsParams(t *testing.T) {
	got := inlineFlags(t, "--type", "command",
		"--param", "check=git status --short",
		"--param", "timeout=45")
	if got.Params["check"] != "git status --short" || got.Params["timeout"] != "45" {
		t.Errorf("params = %+v", got.Params)
	}
}

func TestInlineStepParamNeedsAnEqualsSign(t *testing.T) {
	if err := (paramList{}).Set("check"); err == nil {
		t.Error("expected an error for a param with no '='")
	}
}

func TestInlineStepOmittedPromptStaysDefault(t *testing.T) {
	got := inlineFlags(t, "--type", "failing_prs")
	if got.Prompt != nil || got.MaxOpenPRs != nil {
		t.Errorf("got = %+v", got)
	}
}

func TestInlineStepPromptAndCapAreCarried(t *testing.T) {
	got := inlineFlags(t, "--type", "command", "--prompt", "fix {output}",
		"--max-open-prs", "2")
	if got.Prompt == nil || *got.Prompt != "fix {output}" {
		t.Errorf("prompt = %v", got.Prompt)
	}
	if got.MaxOpenPRs == nil || *got.MaxOpenPRs != 2 {
		t.Errorf("maxOpenPRs = %v", got.MaxOpenPRs)
	}
}

func TestExitCodes(t *testing.T) {
	cases := []struct {
		name   string
		result loop.Result
		want   int
	}{
		{"no work", loop.Result{}, exitOK},
		{"dry run", loop.Result{Step: "a", Prompt: "p"}, exitOK},
		{"session ok", loop.Result{Step: "a", Dispatched: true,
			Outcome: session.OutcomeOK}, exitOK},
		{"session failed", loop.Result{Step: "a", Dispatched: true,
			Outcome: session.OutcomeFailed}, exitSessionFailed},
		{"session exhausted", loop.Result{Step: "a", Dispatched: true,
			Outcome: session.OutcomeTokenExhausted}, exitUsageExhausted},
		{"usage blocked", loop.Result{UsageBlocked: true}, exitUsageExhausted},
	}
	for _, tc := range cases {
		if got := exitCodeFor(tc.result); got != tc.want {
			t.Errorf("%s: exit = %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestUnknownCommandIsAConfigError(t *testing.T) {
	if got := Run([]string{"frobnicate"}); got != exitConfigError {
		t.Errorf("exit = %d", got)
	}
}

func TestStepNeedsANameOrAType(t *testing.T) {
	if got := Run([]string{"step", "--repo", "owner/name"}); got != exitConfigError {
		t.Errorf("exit = %d", got)
	}
}

func TestStepRejectsBothANameAndAType(t *testing.T) {
	got := Run([]string{"step", "--type", "failing_prs", "--repo", "o/n", "some-step"})
	if got != exitConfigError {
		t.Errorf("exit = %d", got)
	}
}

// Go's flag package stops at the first positional word, so flags written
// after the step name have to be lifted back out.
func TestFlagsAfterTheStepNameAreStillParsed(t *testing.T) {
	fs := flag.NewFlagSet("step", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dryRun := fs.Bool("dry-run", false, "")
	cfgPath := fs.String("config", "", "")
	name, err := parseWithOperand(fs,
		[]string{"--config", "a.toml", "failing_prs", "--dry-run"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if name != "failing_prs" || !*dryRun || *cfgPath != "a.toml" {
		t.Errorf("name=%q dryRun=%v config=%q", name, *dryRun, *cfgPath)
	}
	// A flag set before the operand must still count as explicitly set, since
	// resuming the parse means calling Parse more than once.
	if !wasSet(fs, "config") || !wasSet(fs, "dry-run") {
		t.Error("a flag set across the resumed parse was not recorded")
	}
}

func TestOnlyOnePositionalArgumentIsAllowed(t *testing.T) {
	fs := flag.NewFlagSet("step", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if _, err := parseWithOperand(fs, []string{"one", "two"}); err == nil {
		t.Error("expected an error for a second positional argument")
	}
}

func TestHelpSucceeds(t *testing.T) {
	if got := Run([]string{"help"}); got != exitOK {
		t.Errorf("exit = %d", got)
	}
}
