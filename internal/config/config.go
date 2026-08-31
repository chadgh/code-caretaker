// Package config loads and validates agent-loop configuration.
//
// Resolution order for the config file: an explicit --config path, else
// $AGENT_LOOP_CONFIG, else agent_loop.toml at the repo root, else built-in
// defaults. Precedence for [loop] values: env var > TOML > built-in default.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/chadgh/code-caretaker/internal/core"
)

// ConfigFilename is the default config file name looked up at the repo root.
const ConfigFilename = "agent_loop.toml"

// commonStepKeys are the keys every step accepts, regardless of type.
// Everything else in a [[step]] table is passed to the step as Params.
var commonStepKeys = map[string]bool{
	"type": true, "name": true, "enabled": true,
	"prompt": true, "max_open_prs": true,
}

// defaultSteps is used when the config declares no [[step]] tables.
func defaultSteps() []map[string]any {
	return []map[string]any{
		{"type": "failing_prs"},
		{"type": "dependabot_alerts"},
		{"type": "prod_errors"},
		{"type": "labeled_issues", "label": "for-agent", "max_open_prs": int64(3)},
	}
}

// loopField describes one [loop] setting and how to resolve it.
type loopField struct {
	tomlKey string
	envVar  string
	def     any // string or int
	isInt   bool
}

var loopFields = []loopField{
	{"repo", "REPO", "chadgh/class-cash", false},
	{"check_interval_seconds", "CHECK_INTERVAL_SECONDS", 300, true},
	{"token_sleep_seconds", "TOKEN_SLEEP_SECONDS", 3600, true},
	{"claude_timeout_seconds", "CLAUDE_TIMEOUT_SECONDS", 1800, true},
	{"status_file", "AGENT_STATUS_FILE", filepath.Join(".agent-status", "events.jsonl"), false},
}

func cfgErr(format string, args ...any) error {
	return &core.ConfigError{Msg: fmt.Sprintf(format, args...)}
}

// FindConfigPath resolves which config file to read, or "" to use built-in
// defaults.
func FindConfigPath(cliPath, repoRoot string, env map[string]string) (string, error) {
	if cliPath != "" {
		if !isFile(cliPath) {
			return "", cfgErr("Config file not found: %s", cliPath)
		}
		return cliPath, nil
	}
	if envPath, ok := env["AGENT_LOOP_CONFIG"]; ok && envPath != "" {
		if !isFile(envPath) {
			return "", cfgErr(
				"Config file not found (from AGENT_LOOP_CONFIG): %s", envPath)
		}
		return envPath, nil
	}
	defaultPath := filepath.Join(repoRoot, ConfigFilename)
	if isFile(defaultPath) {
		return defaultPath, nil
	}
	return "", nil
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// rawConfig is the raw shape decoded from TOML before validation.
type rawConfig struct {
	Loop map[string]any   `toml:"loop"`
	Step []map[string]any `toml:"step"`
}

func readTOML(path string) (rawConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return rawConfig{}, cfgErr("Failed to read %s: %v", path, err)
	}
	var raw rawConfig
	if _, err := toml.Decode(string(data), &raw); err != nil {
		return rawConfig{}, cfgErr("Failed to parse %s: %v", path, err)
	}
	return raw, nil
}

// coerceInt reproduces Python's int coercion: native ints pass through,
// strings (from env vars) are parsed, and bools/floats are rejected rather
// than silently truncated.
func coerceInt(value any, source string) (int, error) {
	switch v := value.(type) {
	case bool:
		return 0, cfgErr("%s must be an integer, got %s", source, pyRepr(value))
	case int:
		return v, nil
	case int64:
		return int(v), nil
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			return 0, cfgErr("%s must be an integer, got %s", source, pyRepr(value))
		}
		return n, nil
	default:
		return 0, cfgErr("%s must be an integer, got %s", source, pyRepr(value))
	}
}

func coerceStr(value any) string {
	if s, ok := value.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", value)
}

func coerceBool(value any, source string) (bool, error) {
	if b, ok := value.(bool); ok {
		return b, nil
	}
	return false, cfgErr("%s must be a boolean, got %s", source, pyRepr(value))
}

// pyRepr renders a value roughly the way Python's repr would, for error text.
func pyRepr(value any) string {
	switch v := value.(type) {
	case string:
		return "'" + v + "'"
	case bool:
		if v {
			return "True"
		}
		return "False"
	default:
		return fmt.Sprintf("%v", v)
	}
}

func buildLoopConfig(table map[string]any, repoRoot string, env map[string]string) (core.LoopConfig, error) {
	if table == nil {
		table = map[string]any{}
	}
	values := map[string]any{}
	for _, f := range loopFields {
		if envVal, ok := env[f.envVar]; ok {
			if f.isInt {
				n, err := coerceInt(envVal, f.envVar)
				if err != nil {
					return core.LoopConfig{}, err
				}
				values[f.tomlKey] = n
			} else {
				values[f.tomlKey] = coerceStr(envVal)
			}
		} else if tomlVal, ok := table[f.tomlKey]; ok {
			if f.isInt {
				n, err := coerceInt(tomlVal, fmt.Sprintf("[loop].%s", f.tomlKey))
				if err != nil {
					return core.LoopConfig{}, err
				}
				values[f.tomlKey] = n
			} else {
				values[f.tomlKey] = coerceStr(tomlVal)
			}
		} else {
			values[f.tomlKey] = f.def
		}
	}

	statusFile := values["status_file"].(string)
	if !filepath.IsAbs(statusFile) {
		statusFile = filepath.Join(repoRoot, statusFile)
	}

	return core.LoopConfig{
		Repo:                 values["repo"].(string),
		CheckIntervalSeconds: values["check_interval_seconds"].(int),
		TokenSleepSeconds:    values["token_sleep_seconds"].(int),
		ClaudeTimeoutSeconds: values["claude_timeout_seconds"].(int),
		StatusFile:           statusFile,
		RepoRoot:             repoRoot,
	}, nil
}

func buildStepConfig(table map[string]any, index int) (core.StepConfig, error) {
	rawType, ok := table["type"]
	if !ok {
		return core.StepConfig{}, cfgErr(
			"Step #%d is missing required key 'type'.", index+1)
	}
	stepType := coerceStr(rawType)

	var maxOpenPRs *int
	if v, ok := table["max_open_prs"]; ok {
		n, err := coerceInt(v, fmt.Sprintf("step '%s' max_open_prs", stepType))
		if err != nil {
			return core.StepConfig{}, err
		}
		maxOpenPRs = &n
	}

	enabled := true
	if v, ok := table["enabled"]; ok {
		b, err := coerceBool(v, fmt.Sprintf("step '%s' enabled", stepType))
		if err != nil {
			return core.StepConfig{}, err
		}
		enabled = b
	}

	var prompt *string
	if v, ok := table["prompt"]; ok {
		s := coerceStr(v)
		prompt = &s
	}

	name := stepType
	if v, ok := table["name"]; ok {
		name = coerceStr(v)
	}

	params := map[string]any{}
	for k, v := range table {
		if !commonStepKeys[k] {
			params[k] = v
		}
	}

	return core.StepConfig{
		Type:       stepType,
		Name:       name,
		Enabled:    enabled,
		Prompt:     prompt,
		MaxOpenPRs: maxOpenPRs,
		Params:     params,
	}, nil
}

// Load resolves and validates the full configuration. When env is nil the
// process environment is used.
func Load(cliPath string, repoRoot string, env map[string]string) (core.Config, error) {
	if env == nil {
		env = envMap()
	}

	path, err := FindConfigPath(cliPath, repoRoot, env)
	if err != nil {
		return core.Config{}, err
	}

	var raw rawConfig
	if path != "" {
		raw, err = readTOML(path)
		if err != nil {
			return core.Config{}, err
		}
	}

	stepTables := raw.Step
	if len(stepTables) == 0 {
		stepTables = defaultSteps()
	}

	loop, err := buildLoopConfig(raw.Loop, repoRoot, env)
	if err != nil {
		return core.Config{}, err
	}

	var steps []core.StepConfig
	for i, t := range stepTables {
		sc, err := buildStepConfig(t, i)
		if err != nil {
			return core.Config{}, err
		}
		if sc.Enabled {
			steps = append(steps, sc)
		}
	}

	return core.Config{Loop: loop, Steps: steps}, nil
}

func envMap() map[string]string {
	out := map[string]string{}
	for _, kv := range os.Environ() {
		if idx := strings.IndexByte(kv, '='); idx >= 0 {
			out[kv[:idx]] = kv[idx+1:]
		}
	}
	return out
}
