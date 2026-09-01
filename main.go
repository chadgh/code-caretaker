// Command agent_loop is the autonomous agent loop: wake up, run the configured
// steps, sleep.
//
// Steps come from agent_loop.toml (see internal/config for resolution order).
// Each cycle walks them in configured order and dispatches a Claude session
// for the first one that finds work.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/chadgh/code-caretaker/internal/config"
	"github.com/chadgh/code-caretaker/internal/core"
	"github.com/chadgh/code-caretaker/internal/loop"
	"github.com/chadgh/code-caretaker/internal/status"
	"github.com/chadgh/code-caretaker/internal/steps"
)

func main() {
	fs := flag.NewFlagSet("agent_loop", flag.ExitOnError)
	configPath := fs.String("config", "",
		"Path to a config TOML. Defaults to agent_loop.toml at the repo root, "+
			"or built-in defaults if absent.")
	repoRootFlag := fs.String("repo-root", "",
		"Repository root the loop operates in. Defaults to the working directory.")
	_ = fs.Parse(os.Args[1:])

	repoRoot := *repoRootFlag
	if repoRoot == "" {
		wd, err := os.Getwd()
		if err != nil {
			status.Logf("Failed to determine working directory: %v", err)
			os.Exit(2)
		}
		repoRoot = wd
	}

	cfg, err := config.Load(*configPath, repoRoot, nil)
	if err != nil {
		var cfgErr *core.ConfigError
		if errors.As(err, &cfgErr) {
			status.Logf("Configuration error: %s", cfgErr.Msg)
			os.Exit(2)
		}
		status.Logf("Configuration error: %v", err)
		os.Exit(2)
	}

	built, err := steps.Build(cfg.Steps)
	if err != nil {
		var cfgErr *core.ConfigError
		if errors.As(err, &cfgErr) {
			status.Logf("Configuration error: %s", cfgErr.Msg)
			os.Exit(2)
		}
		status.Logf("Configuration error: %v", err)
		os.Exit(2)
	}

	run(built, cfg.Loop)
}

func run(built []core.Step, loopCfg core.LoopConfig) {
	names := make([]string, len(built))
	for i, s := range built {
		names[i] = s.Name()
	}
	stepNames := strings.Join(names, ", ")
	if stepNames == "" {
		stepNames = "none"
	}
	status.Logf("Agent loop started. Repo=%s, interval=%ds, steps=[%s]",
		loopCfg.Repo, loopCfg.CheckIntervalSeconds, stepNames)
	status.Emit(loopCfg.StatusFile, status.Event{
		Event: "startup", Level: "info", Notify: true,
		Summary: fmt.Sprintf("Agent loop started (repo=%s, interval=%ds).",
			loopCfg.Repo, loopCfg.CheckIntervalSeconds),
	})

	for {
		var actions []string
		sleepNormally := true
		func() {
			defer func() {
				if r := recover(); r != nil {
					msg := fmt.Sprintf("Unexpected error in cycle: %v", r)
					status.Log(msg)
					actions = append(actions, "ERROR: "+fmt.Sprint(r))
					status.Emit(loopCfg.StatusFile, status.Event{
						Event: "error", Level: "error", Notify: true, Summary: msg,
						Fields: map[string]any{"actions": actions},
					})
				}
			}()
			sleepNormally = loop.RunOneCycle(built, loopCfg, &actions)
			summary := strings.Join(actions, "; ")
			if summary == "" {
				summary = "Idle cycle."
			}
			status.Emit(loopCfg.StatusFile, status.Event{
				Event: "cycle", Level: "info", Notify: false, Summary: summary,
				Fields: map[string]any{"actions": actions},
			})
		}()

		if sleepNormally {
			time.Sleep(time.Duration(loopCfg.CheckIntervalSeconds) * time.Second)
		}
	}
}
