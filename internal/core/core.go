// Package core holds the shared types that every part of the agent loop
// depends on: the configuration structs, the per-cycle context, the result a
// step returns when it finds work, and the Step interface with its base
// implementation.
//
// It is a leaf-ish package (it only reaches down to status) so that config,
// steps, and the main loop can all share these types without import cycles.
package core

import (
	"github.com/chadgh/code-caretaker/internal/status"
)

// LoopConfig holds the resolved [loop] settings for a run.
type LoopConfig struct {
	Repo                 string
	CheckIntervalSeconds int
	TokenSleepSeconds    int
	ClaudeTimeoutSeconds int
	StatusFile           string
	RepoRoot             string
}

// StepConfig is one [[step]] table after the common keys have been split out
// from the type-specific Params.
type StepConfig struct {
	Type string
	Name string
	// Enabled defaults to true. The loop walks only enabled steps; naming a
	// step explicitly runs it either way.
	Enabled bool
	// Prompt overrides the step's built-in prompt template; nil means "use
	// the default".
	Prompt *string
	// MaxOpenPRs caps the step; nil means no cap.
	MaxOpenPRs *int
	// Params carries every non-common key from the step table, keyed by name.
	Params map[string]any
}

// Config is the fully-resolved configuration for a run. Steps holds every
// declared step in file order, disabled ones included.
type Config struct {
	Loop  LoopConfig
	Steps []StepConfig
}

// EnabledSteps returns the steps the loop should walk, in order, dropping any
// the config disabled.
func (c Config) EnabledSteps() []StepConfig {
	enabled := make([]StepConfig, 0, len(c.Steps))
	for _, s := range c.Steps {
		if s.Enabled {
			enabled = append(enabled, s)
		}
	}
	return enabled
}

// Finding is the work a step found. Prompt is fully rendered and ready to
// dispatch.
//
// PreDispatchEvents are emitted before the Claude session starts; each entry
// is the keyword arguments for status.Emit minus the status file, and must
// carry an "event" key.
//
// Model overrides which Claude model the dispatched session uses; an empty
// string leaves the claude CLI on its default.
type Finding struct {
	Label             string
	Prompt            string
	PreDispatchEvents []status.Event
	Model             string
}

// CycleContext is the state shared by every step within one cycle.
type CycleContext struct {
	Config  LoopConfig
	OpenPRs []map[string]any
}

// Step is one prioritized unit of work in the loop.
type Step interface {
	// Name is the label used in logs and the status feed.
	Name() string
	// MaxOpenPRs returns the open-PR cap, or -1 for no cap.
	MaxOpenPRs() int
	// FindWork returns a Finding if there is work, else nil. Subclass hook.
	FindWork(ctx *CycleContext) *Finding
}

// Check applies the common max_open_prs guard, then delegates to the step's
// FindWork. It is the template method: callers run Check, steps implement
// FindWork.
func Check(s Step, ctx *CycleContext) *Finding {
	if cap := s.MaxOpenPRs(); cap >= 0 && len(ctx.OpenPRs) > cap {
		status.Logf("Step %q skipped: %d open PR(s) exceeds cap of %d.",
			s.Name(), len(ctx.OpenPRs), cap)
		return nil
	}
	return s.FindWork(ctx)
}

// Base is embedded by every concrete step. It stores the common config and
// provides prompt rendering shared across steps.
type Base struct {
	name           string
	maxOpenPRs     int // -1 means no cap
	promptTemplate string
}

// NewBase builds the shared step state from a StepConfig. When the config
// supplies a custom prompt it is validated against placeholders; an unknown
// placeholder or malformed braces returns a ConfigError.
func NewBase(cfg StepConfig, defaultPrompt string, placeholders []string) (Base, error) {
	template := defaultPrompt
	if cfg.Prompt != nil {
		template = *cfg.Prompt
		if err := validatePrompt(cfg.Name, *cfg.Prompt, placeholders); err != nil {
			return Base{}, err
		}
	}
	maxPRs := -1
	if cfg.MaxOpenPRs != nil {
		maxPRs = *cfg.MaxOpenPRs
	}
	return Base{name: cfg.Name, maxOpenPRs: maxPRs, promptTemplate: template}, nil
}

// Name returns the step's label.
func (b *Base) Name() string { return b.name }

// MaxOpenPRs returns the open-PR cap, or -1 for no cap.
func (b *Base) MaxOpenPRs() int { return b.maxOpenPRs }

// Render formats the prompt template with the given values.
//
// Placeholders declares which names are legal in a custom template; it does
// not supply any of them. Callers are responsible for passing every value the
// template needs, including repo.
func (b *Base) Render(values map[string]string) string {
	return formatTemplate(b.promptTemplate, values)
}
