// Package cli parses the command line and runs the requested command.
//
// The default command is the long-running loop. `step` runs a single step —
// one named in the config, or one defined entirely by flags — and exits, which
// is what makes the tool usable from cron, CI, or a one-off shell.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/chadgh/code-caretaker/internal/config"
	"github.com/chadgh/code-caretaker/internal/core"
	"github.com/chadgh/code-caretaker/internal/loop"
	"github.com/chadgh/code-caretaker/internal/session"
	"github.com/chadgh/code-caretaker/internal/status"
	"github.com/chadgh/code-caretaker/internal/steps"
)

// version is stamped at build time with
// -ldflags "-X github.com/chadgh/code-caretaker/internal/cli.version=...".
var version = "dev"

// Exit codes. They are part of the CLI contract: a scheduler reads them to
// decide whether to retry now, back off, or page a human.
const (
	// exitOK covers both "work done" and "nothing to do" — either is success.
	exitOK = 0
	// exitSessionFailed means a Claude session ran and exited non-zero.
	exitSessionFailed = 1
	// exitConfigError means the command never got as far as running a step.
	exitConfigError = 2
	// exitUsageExhausted means Claude has no usage left; retry later.
	exitUsageExhausted = 3
)

const usage = `code-caretaker — an autonomous agent loop for a GitHub repository.

Usage:
  code-caretaker [flags]              Run the loop (same as "run").
  code-caretaker run [flags]          Run the loop until interrupted.
  code-caretaker step [flags] [NAME]  Run one step once, then exit.
  code-caretaker steps [flags]        List the steps the config declares.
  code-caretaker version              Print the version.
  code-caretaker help                 Print this message.

Common flags:
  --config PATH      Config TOML. Default: $AGENT_LOOP_CONFIG, else
                     agent_loop.toml at the repo root, else built-in defaults.
  --repo OWNER/NAME  Repository to operate on. Overrides the config and $REPO.
  --repo-root PATH   Repository checkout to work in. Default: $PWD.
  --no-reset         Skip the "git checkout main && git pull" that normally
                     starts a cycle. Use it when the checkout is already on the
                     ref you want, as in CI.

"step" flags:
  NAME               Run the step with this name (or type) from the config,
                     even if the config disables it.
  --type TYPE        Instead of the config, define a step inline. One of:
                     %s.
  --name NAME        Label for an inline step. Default: its type.
  --prompt TEXT      Prompt template for an inline step. Required for
                     --type=command, optional elsewhere.
  --param KEY=VALUE  A type-specific setting for an inline step. Repeatable.
  --max-open-prs N   Skip the step while more than N pull requests are open.
  --dry-run          Report the work found and print the rendered prompt
                     without dispatching a Claude session.

Exit codes:
  0  the step ran, or found nothing to do
  1  a Claude session ran and failed
  2  configuration or usage error
  3  Claude usage is exhausted; retry later

Environment: REPO, CHECK_INTERVAL_SECONDS, TOKEN_SLEEP_SECONDS,
CLAUDE_TIMEOUT_SECONDS, AGENT_STATUS_FILE, AGENT_LOOP_CONFIG.
`

// Run executes one invocation and returns the process exit code.
func Run(args []string) int {
	command, rest := splitCommand(args)

	switch command {
	case "help", "-h", "--help":
		fmt.Fprintf(os.Stdout, usage, strings.Join(steps.Types(), ", "))
		return exitOK
	case "version", "--version":
		fmt.Fprintln(os.Stdout, version)
		return exitOK
	case "run":
		return runLoop(rest)
	case "step":
		return runStep(rest)
	case "steps":
		return listSteps(rest)
	default:
		fmt.Fprintf(os.Stderr, "Unknown command %q. Run \"code-caretaker help\".\n", command)
		return exitConfigError
	}
}

// splitCommand pulls the leading subcommand off the arguments. Anything that
// starts with a dash means the caller went straight to flags, which is the
// bare "run the loop" form.
func splitCommand(args []string) (string, []string) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return "run", args
	}
	return args[0], args[1:]
}

// commonFlags are the flags every command shares.
type commonFlags struct {
	configPath string
	repo       string
	repoRoot   string
	noReset    bool
}

func (c *commonFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&c.configPath, "config", "", "path to a config TOML")
	fs.StringVar(&c.repo, "repo", "", "repository to operate on, as owner/name")
	fs.StringVar(&c.repoRoot, "repo-root", "", "repository checkout to work in")
	fs.BoolVar(&c.noReset, "no-reset", false,
		"skip the git checkout main && git pull that starts a cycle")
}

// newFlagSet returns a flag set that prints the tool's usage rather than a
// bare flag dump, and that reports errors instead of exiting.
func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

// paramList collects repeated --param KEY=VALUE flags.
type paramList map[string]any

func (p paramList) String() string { return "" }

func (p paramList) Set(value string) error {
	key, val, found := strings.Cut(value, "=")
	if !found || strings.TrimSpace(key) == "" {
		return fmt.Errorf("expected KEY=VALUE, got %q", value)
	}
	p[key] = val
	return nil
}

// resolve turns the shared flags into a loaded configuration.
func (c *commonFlags) resolve() (core.Config, loop.Options, error) {
	repoRoot := c.repoRoot
	if repoRoot == "" {
		wd, err := os.Getwd()
		if err != nil {
			return core.Config{}, loop.Options{}, fmt.Errorf(
				"failed to determine working directory: %w", err)
		}
		repoRoot = wd
	}

	env := config.EnvMap()
	if c.repo != "" {
		env["REPO"] = c.repo
	}

	cfg, err := config.Load(c.configPath, repoRoot, env)
	if err != nil {
		return core.Config{}, loop.Options{}, err
	}
	return cfg, loop.Options{SkipReset: c.noReset}, nil
}

func runLoop(args []string) int {
	fs := newFlagSet("run")
	var common commonFlags
	common.register(fs)
	if err := parseFlags(fs, args); err != nil {
		return reportUsageError(err)
	}

	cfg, opts, err := common.resolve()
	if err != nil {
		return reportConfigError(err)
	}
	built, err := steps.Build(cfg.EnabledSteps())
	if err != nil {
		return reportConfigError(err)
	}

	loop.Run(built, cfg.Loop, opts)
	return exitOK // unreachable: loop.Run does not return.
}

func listSteps(args []string) int {
	fs := newFlagSet("steps")
	var common commonFlags
	common.register(fs)
	if err := parseFlags(fs, args); err != nil {
		return reportUsageError(err)
	}

	cfg, _, err := common.resolve()
	if err != nil {
		return reportConfigError(err)
	}
	if len(cfg.Steps) == 0 {
		fmt.Println("No steps configured.")
		return exitOK
	}
	fmt.Printf("%-24s %-20s %s\n", "NAME", "TYPE", "STATUS")
	for _, sc := range cfg.Steps {
		state := "enabled"
		if !sc.Enabled {
			state = "disabled"
		}
		fmt.Printf("%-24s %-20s %s\n", sc.Name, sc.Type, state)
	}
	return exitOK
}

func runStep(args []string) int {
	fs := newFlagSet("step")
	var common commonFlags
	common.register(fs)
	stepType := fs.String("type", "", "define an inline step of this type")
	stepName := fs.String("name", "", "label for an inline step")
	prompt := fs.String("prompt", "", "prompt template for an inline step")
	maxOpenPRs := fs.Int("max-open-prs", -1, "skip while more than N PRs are open")
	dryRun := fs.Bool("dry-run", false, "print the prompt instead of dispatching")
	params := paramList{}
	fs.Var(params, "param", "a KEY=VALUE setting for an inline step")
	name, err := parseWithOperand(fs, args)
	if err != nil {
		return reportUsageError(err)
	}

	if name != "" && *stepType != "" {
		fmt.Fprintln(os.Stderr,
			"Configuration error: give either a step NAME from the config or "+
				"--type for an inline step, not both.")
		return exitConfigError
	}

	cfg, opts, err := common.resolve()
	if err != nil {
		return reportConfigError(err)
	}
	opts.DryRun = *dryRun

	var stepCfg core.StepConfig
	switch {
	case name != "":
		stepCfg, err = findStep(cfg.Steps, name)
	case *stepType != "":
		stepCfg = inlineStep(*stepType, *stepName, *prompt, params, maxOpenPRs, fs)
	default:
		err = &core.ConfigError{Msg: "Name a step to run, or define one with " +
			"--type. \"code-caretaker steps\" lists what the config declares."}
	}
	if err != nil {
		return reportConfigError(err)
	}

	built, err := steps.Build([]core.StepConfig{stepCfg})
	if err != nil {
		return reportConfigError(err)
	}

	var actions []string
	result := loop.RunOnce(built, cfg.Loop, opts, &actions)

	if opts.DryRun {
		// A dry run leaves no trace, status feed included.
		if result.FoundWork() {
			fmt.Println(result.Prompt)
		}
		return exitCodeFor(result)
	}

	status.Emit(cfg.Loop.StatusFile, status.Event{
		Event: "step", Level: "info", Notify: false,
		Summary: summarize(stepCfg.Name, actions),
		Fields:  map[string]any{"actions": actions, "step": stepCfg.Name},
	})
	return exitCodeFor(result)
}

// exitCodeFor maps a pass over the steps onto the process exit code.
func exitCodeFor(result loop.Result) int {
	switch {
	case result.UsageBlocked:
		return exitUsageExhausted
	case !result.Dispatched:
		return exitOK
	}
	switch result.Outcome {
	case session.OutcomeTokenExhausted:
		return exitUsageExhausted
	case session.OutcomeFailed:
		return exitSessionFailed
	default:
		return exitOK
	}
}

// findStep locates a configured step by name, falling back to its type so that
// `step failing_prs` works whether or not the config renamed it.
func findStep(configured []core.StepConfig, name string) (core.StepConfig, error) {
	for _, sc := range configured {
		if sc.Name == name {
			return sc, nil
		}
	}
	for _, sc := range configured {
		if sc.Type == name {
			return sc, nil
		}
	}
	available := make([]string, 0, len(configured))
	for _, sc := range configured {
		available = append(available, sc.Name)
	}
	sort.Strings(available)
	list := "none"
	if len(available) > 0 {
		list = strings.Join(available, ", ")
	}
	return core.StepConfig{}, &core.ConfigError{Msg: fmt.Sprintf(
		"No step named %q in the config. Available: %s.", name, list)}
}

// inlineStep builds a step config from the flags alone, for running something
// the config never declared.
func inlineStep(stepType, name, prompt string, params paramList,
	maxOpenPRs *int, fs *flag.FlagSet) core.StepConfig {

	if name == "" {
		name = stepType
	}
	sc := core.StepConfig{
		Type:    stepType,
		Name:    name,
		Enabled: true,
		Params:  map[string]any(params),
	}
	if wasSet(fs, "prompt") {
		sc.Prompt = &prompt
	}
	if wasSet(fs, "max-open-prs") {
		sc.MaxOpenPRs = maxOpenPRs
	}
	return sc
}

// wasSet reports whether a flag was given explicitly, so that an omitted flag
// stays "use the default" rather than overriding with a zero value.
func wasSet(fs *flag.FlagSet, name string) bool {
	found := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}

func summarize(step string, actions []string) string {
	if len(actions) == 0 {
		return "Ran step " + step + "."
	}
	return strings.Join(actions, "; ")
}

// parseFlags reads a command that takes no positional arguments.
func parseFlags(fs *flag.FlagSet, args []string) error {
	operand, err := parseWithOperand(fs, args)
	if err != nil {
		return err
	}
	if operand != "" {
		return fmt.Errorf("unexpected argument %q", operand)
	}
	return nil
}

// parseWithOperand parses a command line carrying at most one positional
// argument, which may sit anywhere among the flags. Go's flag package stops at
// the first non-flag word, so `step failing_prs --dry-run` would otherwise
// leave --dry-run unparsed; lift the operand out and keep going.
func parseWithOperand(fs *flag.FlagSet, args []string) (string, error) {
	var operand string
	for {
		if err := fs.Parse(args); err != nil {
			return "", err
		}
		rest := fs.Args()
		if len(rest) == 0 {
			return operand, nil
		}
		if operand != "" {
			return "", fmt.Errorf("unexpected argument %q", rest[0])
		}
		operand, args = rest[0], rest[1:]
	}
}

func reportUsageError(err error) int {
	fmt.Fprintf(os.Stderr, "Configuration error: %v.\n", err)
	return exitConfigError
}

func reportConfigError(err error) int {
	var cfgErr *core.ConfigError
	if errors.As(err, &cfgErr) {
		status.Logf("Configuration error: %s", cfgErr.Msg)
	} else {
		status.Logf("Configuration error: %v", err)
	}
	return exitConfigError
}
