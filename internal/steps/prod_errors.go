package steps

import (
	"context"
	"os/exec"
	"strings"
	"time"

	"github.com/chadgh/code-caretaker/internal/core"
	"github.com/chadgh/code-caretaker/internal/status"
)

// ProdErrorsType is the config `type` for the prod-errors step.
const ProdErrorsType = "prod_errors"

const prodErrorsPrompt = `You are an autonomous agent working on the class-cash GitHub repository ({repo}).

The following errors appeared in production API logs (journalctl {service}) in the last {lookback}:

` + "```" + `
{logs}
` + "```" + `

Steps:
1. Determine whether these errors indicate a code bug or an infrastructure/transient issue.
2. If it is a code bug, implement a fix and create a PR targeting the main branch.
3. If it is infrastructure or transient, print a brief explanation and do not create a PR.

IMPORTANT: Before modifying code locally, ensure you stash changes that are unrelated. Also check stashed changes that might be related.
`

// ProdErrorsStep triages errors in the production API journal.
type ProdErrorsStep struct {
	core.Base
	sshHost  string
	sshUser  string
	service  string
	lookback string
}

func newProdErrorsStep(cfg core.StepConfig) (core.Step, error) {
	base, err := core.NewBase(cfg, prodErrorsPrompt,
		[]string{"repo", "logs", "lookback", "service"})
	if err != nil {
		return nil, err
	}
	return &ProdErrorsStep{
		Base:     base,
		sshHost:  paramStr(cfg.Params, "ssh_host", "class-cash.chaggie.com"),
		sshUser:  paramStr(cfg.Params, "ssh_user", "chadgh"),
		service:  paramStr(cfg.Params, "service", "class-cash-api.service"),
		lookback: paramStr(cfg.Params, "lookback", "5 minutes ago"),
	}, nil
}

// sshRun runs an ssh command with a timeout; a package variable so tests can
// substitute it.
var sshRun = func(args []string, timeout time.Duration) string {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, _ := exec.CommandContext(ctx, "ssh", args...).Output()
	if ctx.Err() == context.DeadlineExceeded {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func getProdErrors(sshHost, sshUser, service, lookback string) string {
	journalCmd := "journalctl -u " + service +
		" --since '" + lookback + "'" +
		" --priority=err" +
		" --no-pager" +
		" --output=short-iso"
	return sshRun([]string{
		"-o", "BatchMode=yes", "-o", "ConnectTimeout=10",
		sshUser + "@" + sshHost, journalCmd,
	}, 30*time.Second)
}

// FindWork returns a Finding when the production journal has recent errors,
// else nil.
func (s *ProdErrorsStep) FindWork(ctx *core.CycleContext) *core.Finding {
	logs := getProdErrors(s.sshHost, s.sshUser, s.service, s.lookback)
	if logs == "" {
		return nil
	}
	status.Log("Found production errors. Dispatching claude session...")
	return &core.Finding{
		Label: "production errors",
		Prompt: s.Render(map[string]string{
			"repo":     ctx.Config.Repo,
			"logs":     logs,
			"lookback": s.lookback,
			"service":  s.service,
		}),
		PreDispatchEvents: []status.Event{{
			Event:   "prod_errors",
			Level:   "error",
			Notify:  true,
			Summary: "Production API errors detected in the last " + s.lookback + ".",
			Fields:  map[string]any{"logs": tailStr(logs, 2000)},
		}},
	}
}

func tailStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
