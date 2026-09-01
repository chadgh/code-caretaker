package steps

import (
	"context"
	"os/exec"
	"strings"
	"time"

	"github.com/chadgh/code-caretaker/internal/core"
	"github.com/chadgh/code-caretaker/internal/status"
)

// CommandType is the config `type` for the generic command step.
const CommandType = "command"

// CommandStep is the generic escape hatch: it runs a shell command, and if the
// command prints anything, that output becomes {output} in the configured
// prompt and a session is dispatched. Use this to try a new step without
// writing Go — promote it to a real step type if it sticks.
type CommandStep struct {
	core.Base
	checkCommand string
	timeout      int
	model        string
}

func newCommandStep(cfg core.StepConfig) (core.Step, error) {
	if cfg.Prompt == nil || *cfg.Prompt == "" {
		return nil, &core.ConfigError{Msg: "Step '" + cfg.Name +
			"' (type=command) requires a 'prompt' — there is no default prompt " +
			"for a command step."}
	}
	base, err := core.NewBase(cfg, "", []string{"repo", "output"})
	if err != nil {
		return nil, err
	}
	check := paramStr(cfg.Params, "check", "")
	if check == "" {
		return nil, &core.ConfigError{Msg: "Step '" + cfg.Name +
			"' (type=command) requires a 'check' command to run."}
	}
	timeout := 30
	if v, ok := paramInt(cfg.Params, "timeout"); ok {
		timeout = v
	}
	model, err := resolveModel(cfg)
	if err != nil {
		return nil, err
	}
	return &CommandStep{Base: base, checkCommand: check, timeout: timeout, model: model}, nil
}

func resolveModel(cfg core.StepConfig) (string, error) {
	v, ok := cfg.Params["model"]
	if !ok {
		return "", nil
	}
	s, ok := v.(string)
	if !ok || strings.TrimSpace(s) == "" {
		return "", &core.ConfigError{Msg: "Step '" + cfg.Name +
			"' (type=command): 'model' must be a non-empty string."}
	}
	return s, nil
}

// runCheck runs the shell check command; a package variable so tests can
// substitute it.
var runCheck = func(command, repoRoot string, timeout int) string {
	ctx, cancel := context.WithTimeout(context.Background(),
		time.Duration(timeout)*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = repoRoot
	out, _ := cmd.Output()
	if ctx.Err() == context.DeadlineExceeded {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// FindWork runs the check command and returns a Finding when it prints output.
func (s *CommandStep) FindWork(ctx *core.CycleContext) *core.Finding {
	output := runCheck(s.checkCommand, ctx.Config.RepoRoot, s.timeout)
	if output == "" {
		return nil
	}
	status.Logf("Step '%s' check produced output.", s.Name())
	return &core.Finding{
		Label: s.Name(),
		Prompt: s.Render(map[string]string{
			"repo":   ctx.Config.Repo,
			"output": output,
		}),
		Model: s.model,
	}
}
