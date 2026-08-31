// Package steps holds the concrete loop steps and the registry that maps a
// config `type` string to its constructor.
package steps

import (
	"fmt"
	"sort"

	"github.com/chadgh/code-caretaker/internal/core"
)

// stepConstructor builds a Step from its validated config.
type stepConstructor func(core.StepConfig) (core.Step, error)

// stepTypes maps a config `type` to its constructor.
var stepTypes = map[string]stepConstructor{
	FailingPrsType:    newFailingPrsStep,
	DependabotType:    newDependabotStep,
	ProdErrorsType:    newProdErrorsStep,
	LabeledIssuesType: newLabeledIssuesStep,
	CommandType:       newCommandStep,
}

// Build instantiates configured steps in order. It returns a ConfigError at
// startup for an unknown step type.
func Build(stepConfigs []core.StepConfig) ([]core.Step, error) {
	built := make([]core.Step, 0, len(stepConfigs))
	for _, cfg := range stepConfigs {
		ctor, ok := stepTypes[cfg.Type]
		if !ok {
			return nil, &core.ConfigError{Msg: fmt.Sprintf(
				"Unknown step type '%s'. Valid types: %s.",
				cfg.Type, validTypes())}
		}
		step, err := ctor(cfg)
		if err != nil {
			return nil, err
		}
		built = append(built, step)
	}
	return built, nil
}

func validTypes() string {
	types := make([]string, 0, len(stepTypes))
	for t := range stepTypes {
		types = append(types, t)
	}
	sort.Strings(types)
	quoted := make([]string, len(types))
	for i, t := range types {
		quoted[i] = "'" + t + "'"
	}
	return "[" + join(quoted, ", ") + "]"
}

func join(items []string, sep string) string {
	out := ""
	for i, it := range items {
		if i > 0 {
			out += sep
		}
		out += it
	}
	return out
}
