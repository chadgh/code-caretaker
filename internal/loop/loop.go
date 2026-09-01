// Package loop drives the agent cycle: wake up, run the configured steps in
// priority order, dispatch a Claude session for the first that finds work,
// then report whether the caller should sleep normally.
package loop

import (
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

// RunOneCycle runs one cycle. It returns true if the caller should sleep
// normally, appending a human-readable log of what happened to actions.
func RunOneCycle(steps []core.Step, cfg core.LoopConfig, actions *[]string) bool {
	resetToMain(cfg.RepoRoot)

	if !usageAvailable() {
		msg := "Skipped: claude usage limit exceeded. Sleeping before next cycle."
		*actions = append(*actions, msg)
		status.Log(msg)
		status.Emit(cfg.StatusFile, status.Event{
			Event: "usage_limit", Level: "warn", Notify: true, Summary: msg,
		})
		sleep(time.Duration(cfg.TokenSleepSeconds) * time.Second)
		return false
	}

	ctx := &core.CycleContext{Config: cfg, OpenPRs: getOpenPRs(cfg.Repo)}

	for _, step := range steps {
		finding := core.Check(step, ctx)
		if finding == nil {
			continue
		}

		for _, ev := range finding.PreDispatchEvents {
			status.Emit(cfg.StatusFile, ev)
		}

		*actions = append(*actions, "Dispatching Claude session for "+finding.Label+".")
		result := runClaudeSession(finding.Prompt, cfg.RepoRoot,
			cfg.ClaudeTimeoutSeconds, finding.Model)
		slept := handleSession(finding.Label, result, actions,
			cfg.StatusFile, cfg.TokenSleepSeconds)
		// If we already slept for token exhaustion, don't sleep again.
		return !slept
	}

	*actions = append(*actions, "Nothing to do this cycle.")
	status.Log("Nothing to do this cycle.")
	return true
}
