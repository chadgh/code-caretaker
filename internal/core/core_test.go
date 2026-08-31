package core

import (
	"strings"
	"testing"
)

// fakeStep mirrors the Python test's _FakeStep: default prompt with {repo} and
// {thing}, rendering "something" for thing.
type fakeStep struct {
	Base
}

var fakePlaceholders = []string{"repo", "thing"}

const fakeDefaultPrompt = "default prompt for {repo} about {thing}"

func newFake(t *testing.T, cfg StepConfig) *fakeStep {
	t.Helper()
	base, err := NewBase(cfg, fakeDefaultPrompt, fakePlaceholders)
	if err != nil {
		t.Fatalf("NewBase: %v", err)
	}
	return &fakeStep{Base: base}
}

func newFakeErr(cfg StepConfig) error {
	_, err := NewBase(cfg, fakeDefaultPrompt, fakePlaceholders)
	return err
}

func (s *fakeStep) FindWork(ctx *CycleContext) *Finding {
	return &Finding{
		Label:  "fake work",
		Prompt: s.Render(map[string]string{"repo": ctx.Config.Repo, "thing": "something"}),
	}
}

func stepConfig(typ string, mods ...func(*StepConfig)) StepConfig {
	cfg := StepConfig{Type: typ, Name: typ, Enabled: true}
	for _, m := range mods {
		m(&cfg)
	}
	return cfg
}

func withPrompt(p string) func(*StepConfig) {
	return func(c *StepConfig) { c.Prompt = &p }
}
func withName(n string) func(*StepConfig) {
	return func(c *StepConfig) { c.Name = n }
}
func withMaxPRs(n int) func(*StepConfig) {
	return func(c *StepConfig) { c.MaxOpenPRs = &n }
}

func makeCtx(prs ...map[string]any) *CycleContext {
	return &CycleContext{Config: LoopConfig{Repo: "chadgh/class-cash"}, OpenPRs: prs}
}

func TestStepUsesDefaultPromptWhenNoOverride(t *testing.T) {
	step := newFake(t, stepConfig("fake"))
	got := Check(step, makeCtx()).Prompt
	if got != "default prompt for chadgh/class-cash about something" {
		t.Errorf("prompt = %q", got)
	}
}

func TestStepUsesCustomPromptWhenProvided(t *testing.T) {
	step := newFake(t, stepConfig("fake", withPrompt("custom {thing} in {repo}")))
	got := Check(step, makeCtx()).Prompt
	if got != "custom something in chadgh/class-cash" {
		t.Errorf("prompt = %q", got)
	}
}

func TestCustomPromptWithUnknownPlaceholderRaises(t *testing.T) {
	err := newFakeErr(stepConfig("fake", withPrompt("uses {nonsense}")))
	if err == nil || !strings.Contains(err.Error(), "unknown placeholder") {
		t.Errorf("err = %v", err)
	}
}

func TestCustomPromptErrorNamesStepAndValidPlaceholders(t *testing.T) {
	err := newFakeErr(stepConfig("fake", withName("my-step"), withPrompt("uses {nonsense}")))
	if err == nil || !strings.Contains(err.Error(), "my-step") || !strings.Contains(err.Error(), "thing") {
		t.Errorf("err = %v", err)
	}
}

func TestCustomPromptPositionalAccepted(t *testing.T) {
	if err := newFakeErr(stepConfig("fake", withPrompt("{0}"))); err != nil {
		t.Errorf("err = %v", err)
	}
}

func TestCustomPromptAutoNumberedAccepted(t *testing.T) {
	if err := newFakeErr(stepConfig("fake", withPrompt("{}"))); err != nil {
		t.Errorf("err = %v", err)
	}
}

func TestCustomPromptEscapedBracesAccepted(t *testing.T) {
	if err := newFakeErr(stepConfig("fake", withPrompt("{{literal}}"))); err != nil {
		t.Errorf("err = %v", err)
	}
}

func TestCustomPromptMalformedBracesRaises(t *testing.T) {
	err := newFakeErr(stepConfig("fake", withPrompt("answer {repo")))
	if err == nil || !strings.Contains(err.Error(), "malformed") {
		t.Errorf("err = %v", err)
	}
}

func TestStepNameIsExposed(t *testing.T) {
	step := newFake(t, stepConfig("fake", withName("ci-babysitter")))
	if step.Name() != "ci-babysitter" {
		t.Errorf("name = %q", step.Name())
	}
}

func TestCheckReturnsFindingUnderPRCap(t *testing.T) {
	step := newFake(t, stepConfig("fake", withMaxPRs(3)))
	if Check(step, makeCtx(map[string]any{}, map[string]any{}, map[string]any{})) == nil {
		t.Error("expected finding under cap")
	}
}

func TestCheckShortCircuitsOverPRCap(t *testing.T) {
	step := newFake(t, stepConfig("fake", withMaxPRs(3)))
	if Check(step, makeCtx(map[string]any{}, map[string]any{}, map[string]any{}, map[string]any{})) != nil {
		t.Error("expected nil over cap")
	}
}

func TestCheckIgnoresPRCountWhenCapUnset(t *testing.T) {
	step := newFake(t, stepConfig("fake"))
	prs := make([]map[string]any, 99)
	if Check(step, makeCtx(prs...)) == nil {
		t.Error("expected finding when cap unset")
	}
}

func TestEscapedBracesRenderAsLiterals(t *testing.T) {
	step := newFake(t, stepConfig("fake", withPrompt("{{repo}} is {repo}")))
	got := step.Render(map[string]string{"repo": "x"})
	if got != "{repo} is x" {
		t.Errorf("render = %q", got)
	}
}
