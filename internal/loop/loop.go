// Package loop drives the agent cycle: wake up, run the configured steps in
// priority order, and dispatch a Claude session for the first that finds work.
//
// RunOnce is a single pass and does no waiting — it reports what happened and
// leaves the pacing to the caller, so the same code backs the long-running
// loop and the one-shot `step` command. Run is the long-running loop built on
// top of it.
package loop

import (
	"fmt"
	"strings"
	"time"

	"github.com/chadgh/code-caretaker/internal/core"
	"github.com/chadgh/code-caretaker/internal/gh"
	"github.com/chadgh/code-caretaker/internal/git"
	"github.com/chadgh/code-caretaker/internal/session"
	"github.com/chadgh/code-caretaker/internal/status"
)

// Collaborators are package variables so tests can substitute them.
var (
	resetToMain      = git.ResetToMain
	usageAvailable   = session.UsageAvailable
	getOpenPRs       = gh.GetOpenPRs
	runClaudeSession = session.RunClaudeSession
	handleSession    = session.HandleSession
	sleep            = time.Sleep
)

// Options tunes a pass over the steps.
type Options struct {
	// SkipReset leaves the working tree as it is instead of returning it to a
	// clean main before the steps run.
	SkipReset bool
	// DryRun renders the prompt for the work found but dispatches no session.
	// It also skips the reset and the Claude usage check, so it stays free of
	// side effects and works without Claude installed.
	DryRun bool
}

// Result reports what one pass over the steps did.
type Result struct {
	// Step is the name of the step that found work; empty when none did.
	Step string
	// Label describes the specific work found, e.g. "issue #12: Add a flag".
	Label string
	// Prompt is the rendered prompt for that work.
	Prompt string
	// Dispatched is true when a Claude session actually ran.
	Dispatched bool
	// Outcome is how that session ended; meaningful only when Dispatched.
	Outcome session.Outcome
	// UsageBlocked is true when Claude had no usage left, so no step ran.
	UsageBlocked bool
}

// FoundWork reports whether any step found work in this pass.
func (r Result) FoundWork() bool { return r.Step != "" }

// Backoff reports whether the caller should wait TokenSleepSeconds rather than
// the normal interval before trying again.
func (r Result) Backoff() bool {
	return r.UsageBlocked ||
		(r.Dispatched && r.Outcome == session.OutcomeTokenExhausted)
}

// RunOnce makes a single pass over the steps: reset to main, confirm usage
// remains, fetch the open PRs, then walk the steps in order and dispatch a
// session for the first that finds work. It never sleeps; the caller decides
// what to do with the Result. A human-readable log of what happened is
// appended to actions.
func RunOnce(steps []core.Step, cfg core.LoopConfig, opts Options, actions *[]string) Result {
	if !opts.SkipReset && !opts.DryRun {
		resetToMain(cfg.RepoRoot)
	}

	if !opts.DryRun && !usageAvailable() {
		msg := "Skipped: claude usage limit exceeded."
		*actions = append(*actions, msg)
		status.Log(msg)
		status.Emit(cfg.StatusFile, status.Event{
			Event: "usage_limit", Level: "warn", Notify: true, Summary: msg,
		})
		return Result{UsageBlocked: true}
	}

	ctx := &core.CycleContext{Config: cfg, OpenPRs: getOpenPRs(cfg.Repo)}

	for _, step := range steps {
		finding := core.Check(step, ctx)
		if finding == nil {
			continue
		}

		res := Result{Step: step.Name(), Label: finding.Label, Prompt: finding.Prompt}

		if opts.DryRun {
			msg := "Dry run: " + finding.Label + " has work; no session dispatched."
			*actions = append(*actions, msg)
			status.Log(msg)
			return res
		}

		for _, ev := range finding.PreDispatchEvents {
			status.Emit(cfg.StatusFile, ev)
		}

		*actions = append(*actions, "Dispatching Claude session for "+finding.Label+".")
		sessionResult := runClaudeSession(finding.Prompt, cfg.RepoRoot,
			cfg.ClaudeTimeoutSeconds, finding.Model)
		res.Dispatched = true
		res.Outcome = handleSession(finding.Label, sessionResult, actions, cfg.StatusFile)
		return res
	}

	*actions = append(*actions, "Nothing to do.")
	status.Log("Nothing to do.")
	return Result{}
}

// Run is the long-running loop: a pass over the steps, a status record, a
// sleep, forever. It backs off for TokenSleepSeconds instead of the normal
// interval whenever Claude usage has run out. It does not return.
func Run(steps []core.Step, cfg core.LoopConfig, opts Options) {
	names := make([]string, len(steps))
	for i, s := range steps {
		names[i] = s.Name()
	}
	stepNames := strings.Join(names, ", ")
	if stepNames == "" {
		stepNames = "none"
	}
	status.Logf("Agent loop started. Repo=%s, interval=%ds, steps=[%s]",
		cfg.Repo, cfg.CheckIntervalSeconds, stepNames)
	status.Emit(cfg.StatusFile, status.Event{
		Event: "startup", Level: "info", Notify: true,
		Summary: fmt.Sprintf("Agent loop started (repo=%s, interval=%ds).",
			cfg.Repo, cfg.CheckIntervalSeconds),
	})

	for {
		backoff := runCycle(steps, cfg, opts)
		delay := cfg.CheckIntervalSeconds
		if backoff {
			delay = cfg.TokenSleepSeconds
			status.Logf("Backing off %ds before the next cycle.", delay)
		}
		sleep(time.Duration(delay) * time.Second)
	}
}

// runCycle is one iteration of Run: a pass over the steps plus its status
// record, with a panic in a step contained to this cycle. It reports whether
// the caller should back off.
func runCycle(steps []core.Step, cfg core.LoopConfig, opts Options) (backoff bool) {
	var actions []string
	defer func() {
		if r := recover(); r != nil {
			msg := fmt.Sprintf("Unexpected error in cycle: %v", r)
			status.Log(msg)
			actions = append(actions, "ERROR: "+fmt.Sprint(r))
			status.Emit(cfg.StatusFile, status.Event{
				Event: "error", Level: "error", Notify: true, Summary: msg,
				Fields: map[string]any{"actions": actions},
			})
			backoff = false
		}
	}()

	result := RunOnce(steps, cfg, opts, &actions)
	summary := strings.Join(actions, "; ")
	if summary == "" {
		summary = "Idle cycle."
	}
	status.Emit(cfg.StatusFile, status.Event{
		Event: "cycle", Level: "info", Notify: false, Summary: summary,
		Fields: map[string]any{"actions": actions},
	})
	return result.Backoff()
}
