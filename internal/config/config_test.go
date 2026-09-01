package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chadgh/code-caretaker/internal/core"
)

func write(t *testing.T, dir, text string) string {
	t.Helper()
	path := filepath.Join(dir, "agent_loop.toml")
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func stepTypes(cfg core.Config) []string {
	out := make([]string, len(cfg.Steps))
	for i, s := range cfg.Steps {
		out[i] = s.Type
	}
	return out
}

func mustLoad(t *testing.T, cliPath, repoRoot string, env map[string]string) core.Config {
	t.Helper()
	cfg, err := Load(cliPath, repoRoot, env)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return cfg
}

// env builds an environment that supplies the required REPO setting, plus any
// extra key/value pairs, for tests that are not about repo resolution.
func env(extra ...string) map[string]string {
	out := map[string]string{"REPO": "owner/repo"}
	for i := 0; i+1 < len(extra); i += 2 {
		out[extra[i]] = extra[i+1]
	}
	return out
}

func TestDefaultsApplyWhenNoConfigFile(t *testing.T) {
	cfg := mustLoad(t, "", t.TempDir(), env())
	if cfg.Loop.Repo != "owner/repo" {
		t.Errorf("repo = %q", cfg.Loop.Repo)
	}
	if cfg.Loop.CheckIntervalSeconds != 300 || cfg.Loop.TokenSleepSeconds != 3600 ||
		cfg.Loop.ClaudeTimeoutSeconds != 1800 {
		t.Errorf("loop ints wrong: %+v", cfg.Loop)
	}
	got := stepTypes(cfg)
	want := []string{"failing_prs", "dependabot_alerts", "prod_errors", "labeled_issues"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("steps = %v, want %v", got, want)
	}
}

func TestMissingRepoIsAnError(t *testing.T) {
	_, err := Load("", t.TempDir(), map[string]string{})
	expectConfigErr(t, err, "[loop].repo is required")
}

func TestDefaultStatusFileIsAbsoluteUnderRepoRoot(t *testing.T) {
	dir := t.TempDir()
	cfg := mustLoad(t, "", dir, env())
	want := filepath.Join(dir, ".agent-status", "events.jsonl")
	if cfg.Loop.StatusFile != want {
		t.Errorf("status_file = %q, want %q", cfg.Loop.StatusFile, want)
	}
}

func TestTomlOverridesDefaults(t *testing.T) {
	dir := t.TempDir()
	path := write(t, dir, `
[loop]
repo = "other/repo"
check_interval_seconds = 60

[[step]]
type = "failing_prs"
`)
	cfg := mustLoad(t, path, dir, map[string]string{})
	if cfg.Loop.Repo != "other/repo" || cfg.Loop.CheckIntervalSeconds != 60 {
		t.Errorf("overrides not applied: %+v", cfg.Loop)
	}
	if cfg.Loop.TokenSleepSeconds != 3600 {
		t.Errorf("untouched default changed: %d", cfg.Loop.TokenSleepSeconds)
	}
}

func TestEnvOverridesToml(t *testing.T) {
	dir := t.TempDir()
	path := write(t, dir, `
[loop]
repo = "other/repo"
check_interval_seconds = 60

[[step]]
type = "failing_prs"
`)
	cfg := mustLoad(t, path, dir, env("REPO", "env/repo", "CHECK_INTERVAL_SECONDS", "5"))
	if cfg.Loop.Repo != "env/repo" || cfg.Loop.CheckIntervalSeconds != 5 {
		t.Errorf("env override failed: %+v", cfg.Loop)
	}
}

func TestStatusFileRelativeResolvesAgainstRepoRoot(t *testing.T) {
	dir := t.TempDir()
	path := write(t, dir, `
[loop]
status_file = "feed/events.jsonl"

[[step]]
type = "failing_prs"
`)
	cfg := mustLoad(t, path, dir, env())
	want := filepath.Join(dir, "feed", "events.jsonl")
	if cfg.Loop.StatusFile != want {
		t.Errorf("status_file = %q, want %q", cfg.Loop.StatusFile, want)
	}
}

func TestStatusFileAbsoluteIsKept(t *testing.T) {
	dir := t.TempDir()
	path := write(t, dir, `
[loop]
status_file = "/var/log/feed.jsonl"

[[step]]
type = "failing_prs"
`)
	cfg := mustLoad(t, path, dir, env())
	if cfg.Loop.StatusFile != "/var/log/feed.jsonl" {
		t.Errorf("status_file = %q", cfg.Loop.StatusFile)
	}
}

func TestStepsPreserveFileOrder(t *testing.T) {
	dir := t.TempDir()
	path := write(t, dir, `
[[step]]
type = "prod_errors"

[[step]]
type = "failing_prs"
`)
	got := stepTypes(mustLoad(t, path, dir, env()))
	if strings.Join(got, ",") != "prod_errors,failing_prs" {
		t.Errorf("order = %v", got)
	}
}

const disabledFirstStep = `
[[step]]
type = "failing_prs"
enabled = false

[[step]]
type = "prod_errors"
`

func TestDisabledStepsAreDroppedFromTheLoop(t *testing.T) {
	dir := t.TempDir()
	path := write(t, dir, disabledFirstStep)
	cfg := mustLoad(t, path, dir, env())
	var got []string
	for _, s := range cfg.EnabledSteps() {
		got = append(got, s.Type)
	}
	if strings.Join(got, ",") != "prod_errors" {
		t.Errorf("got = %v", got)
	}
}

// Load keeps disabled steps so that naming one on the command line can still
// run it.
func TestDisabledStepsAreStillLoaded(t *testing.T) {
	dir := t.TempDir()
	path := write(t, dir, disabledFirstStep)
	cfg := mustLoad(t, path, dir, env())
	got := stepTypes(cfg)
	if strings.Join(got, ",") != "failing_prs,prod_errors" {
		t.Errorf("got = %v", got)
	}
	if cfg.Steps[0].Enabled {
		t.Error("expected the first step to be marked disabled")
	}
}

func TestStepNameDefaultsToType(t *testing.T) {
	dir := t.TempDir()
	path := write(t, dir, "[[step]]\ntype = \"failing_prs\"\n")
	cfg := mustLoad(t, path, dir, env())
	if cfg.Steps[0].Name != "failing_prs" {
		t.Errorf("name = %q", cfg.Steps[0].Name)
	}
}

func TestStepNameCanBeOverridden(t *testing.T) {
	dir := t.TempDir()
	path := write(t, dir, "[[step]]\ntype = \"failing_prs\"\nname = \"ci-babysitter\"\n")
	cfg := mustLoad(t, path, dir, env())
	if cfg.Steps[0].Name != "ci-babysitter" {
		t.Errorf("name = %q", cfg.Steps[0].Name)
	}
}

func TestCommonKeysAreSeparatedFromParams(t *testing.T) {
	dir := t.TempDir()
	path := write(t, dir, `
[[step]]
type = "labeled_issues"
name = "issues"
max_open_prs = 3
label = "for-agent"
`)
	step := mustLoad(t, path, dir, env()).Steps[0]
	if step.MaxOpenPRs == nil || *step.MaxOpenPRs != 3 {
		t.Errorf("max_open_prs = %v", step.MaxOpenPRs)
	}
	if len(step.Params) != 1 || step.Params["label"] != "for-agent" {
		t.Errorf("params = %v", step.Params)
	}
}

func TestMaxOpenPRsDefaultsToNil(t *testing.T) {
	dir := t.TempDir()
	path := write(t, dir, "[[step]]\ntype = \"failing_prs\"\n")
	cfg := mustLoad(t, path, dir, env())
	if cfg.Steps[0].MaxOpenPRs != nil {
		t.Errorf("max_open_prs = %v", cfg.Steps[0].MaxOpenPRs)
	}
}

func expectConfigErr(t *testing.T, err error, substr string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error containing %q, got nil", substr)
	}
	var cfgErr *core.ConfigError
	if !asConfigError(err, &cfgErr) {
		t.Fatalf("expected ConfigError, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), substr) {
		t.Errorf("error %q does not contain %q", err.Error(), substr)
	}
}

func asConfigError(err error, target **core.ConfigError) bool {
	ce, ok := err.(*core.ConfigError)
	if ok {
		*target = ce
	}
	return ok
}

func TestStepWithoutTypeRaises(t *testing.T) {
	dir := t.TempDir()
	path := write(t, dir, "[[step]]\nname = \"nameless\"\n")
	_, err := Load(path, dir, env())
	expectConfigErr(t, err, "missing required key 'type'")
}

func TestExplicitMissingConfigPathRaises(t *testing.T) {
	dir := t.TempDir()
	_, err := Load(filepath.Join(dir, "nope.toml"), dir, env())
	expectConfigErr(t, err, "not found")
}

func TestMalformedTomlRaisesConfigError(t *testing.T) {
	dir := t.TempDir()
	path := write(t, dir, "this is not = = toml")
	_, err := Load(path, dir, env())
	expectConfigErr(t, err, "Failed to parse")
}

func TestNonIntegerEnvOverrideRaises(t *testing.T) {
	dir := t.TempDir()
	_, err := Load("", dir, env("CHECK_INTERVAL_SECONDS", "soon"))
	expectConfigErr(t, err, "CHECK_INTERVAL_SECONDS")
}

func TestRepoRootConfigFileFoundAutomatically(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "[loop]\nrepo = \"found/me\"\n\n[[step]]\ntype = \"failing_prs\"\n")
	cfg := mustLoad(t, "", dir, map[string]string{})
	if cfg.Loop.Repo != "found/me" {
		t.Errorf("repo = %q", cfg.Loop.Repo)
	}
}

func TestAgentLoopConfigEnvVarSelectsFile(t *testing.T) {
	dir := t.TempDir()
	other := filepath.Join(dir, "other.toml")
	if err := os.WriteFile(other, []byte("[loop]\nrepo = \"via/env\"\n\n[[step]]\ntype = \"failing_prs\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := mustLoad(t, "", dir, map[string]string{"AGENT_LOOP_CONFIG": other})
	if cfg.Loop.Repo != "via/env" {
		t.Errorf("repo = %q", cfg.Loop.Repo)
	}
}

func TestTomlFloatForIntFieldRaises(t *testing.T) {
	dir := t.TempDir()
	path := write(t, dir, "[loop]\ncheck_interval_seconds = 300.7\n\n[[step]]\ntype = \"failing_prs\"\n")
	_, err := Load(path, dir, env())
	expectConfigErr(t, err, "[loop].check_interval_seconds")
}

func TestTomlBoolForIntFieldRaises(t *testing.T) {
	dir := t.TempDir()
	path := write(t, dir, "[loop]\ncheck_interval_seconds = true\n\n[[step]]\ntype = \"failing_prs\"\n")
	_, err := Load(path, dir, env())
	expectConfigErr(t, err, "[loop].check_interval_seconds")
}

func TestTomlFloatForMaxOpenPRsRaises(t *testing.T) {
	dir := t.TempDir()
	path := write(t, dir, "[[step]]\ntype = \"labeled_issues\"\nmax_open_prs = 3.9\n")
	_, err := Load(path, dir, env())
	expectConfigErr(t, err, "max_open_prs")
}

func TestTomlStringEnabledRaises(t *testing.T) {
	dir := t.TempDir()
	path := write(t, dir, "[[step]]\ntype = \"failing_prs\"\nenabled = \"false\"\n")
	_, err := Load(path, dir, env())
	expectConfigErr(t, err, "enabled must be a boolean")
}

func TestEnvStringOverrideStillWorks(t *testing.T) {
	dir := t.TempDir()
	cfg := mustLoad(t, "", dir, env("CHECK_INTERVAL_SECONDS", "5"))
	if cfg.Loop.CheckIntervalSeconds != 5 {
		t.Errorf("check_interval = %d", cfg.Loop.CheckIntervalSeconds)
	}
}

func TestTomlValidIntStillWorks(t *testing.T) {
	dir := t.TempDir()
	path := write(t, dir, "[loop]\ncheck_interval_seconds = 60\n\n[[step]]\ntype = \"failing_prs\"\n")
	cfg := mustLoad(t, path, dir, env())
	if cfg.Loop.CheckIntervalSeconds != 60 {
		t.Errorf("check_interval = %d", cfg.Loop.CheckIntervalSeconds)
	}
}
