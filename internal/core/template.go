package core

import (
	"fmt"
	"sort"
	"strings"
)

// The step prompts are Python str.format templates: {name} is a named field,
// {{ and }} are literal braces, and {} / {0} are anonymous or positional
// fields. To keep TOML-authored prompts behaving exactly as they did under
// Python, we reimplement just enough of that grammar here: a validator that
// rejects unknown named placeholders and malformed braces at load time, and a
// renderer that substitutes named fields.

// parsedField is one {...} replacement field found in a template.
type parsedField struct {
	name string // the field name, before any ! conversion or : format spec
}

// parseTemplate walks a str.format-style template, returning the named
// replacement fields it contains. It errors on malformed braces (a lone { or
// }), mirroring Python's Formatter.parse.
func parseTemplate(template string) ([]parsedField, error) {
	var fields []parsedField
	runes := []rune(template)
	i := 0
	n := len(runes)
	for i < n {
		c := runes[i]
		switch c {
		case '{':
			if i+1 < n && runes[i+1] == '{' {
				i += 2 // escaped literal brace
				continue
			}
			// Find the closing brace.
			j := i + 1
			for j < n && runes[j] != '}' {
				j++
			}
			if j >= n {
				return nil, fmt.Errorf(
					"expected '}' before end of string")
			}
			field := string(runes[i+1 : j])
			// A field name ends at the first ! (conversion) or : (format
			// spec), matching Python's field-name grammar.
			name := field
			if idx := strings.IndexAny(name, "!:"); idx >= 0 {
				name = name[:idx]
			}
			fields = append(fields, parsedField{name: name})
			i = j + 1
		case '}':
			if i+1 < n && runes[i+1] == '}' {
				i += 2 // escaped literal brace
				continue
			}
			return nil, fmt.Errorf("Single '}' encountered in format string")
		default:
			i++
		}
	}
	return fields, nil
}

// validatePrompt confirms every named placeholder in a custom template is one
// the step declares. Empty (anonymous {}) and purely numeric (positional {0})
// field names are not named placeholders, so they are allowed.
func validatePrompt(stepName, template string, placeholders []string) error {
	fields, err := parseTemplate(template)
	if err != nil {
		return &ConfigError{fmt.Sprintf(
			"Step '%s': prompt is malformed: %s. "+
				"(Literal braces must be escaped as {{ and }}.)",
			stepName, err)}
	}
	allowed := make(map[string]bool, len(placeholders))
	for _, p := range placeholders {
		allowed[p] = true
	}
	var unknown []string
	seen := map[string]bool{}
	for _, f := range fields {
		if f.name == "" || isAllDigits(f.name) {
			continue
		}
		if !allowed[f.name] && !seen[f.name] {
			seen[f.name] = true
			unknown = append(unknown, f.name)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		valid := append([]string(nil), placeholders...)
		sort.Strings(valid)
		return &ConfigError{fmt.Sprintf(
			"Step '%s': prompt uses unknown placeholder(s) %s. "+
				"Valid placeholders: %s. "+
				"(Literal braces must be escaped as {{ and }}.)",
			stepName, formatList(unknown), formatList(valid))}
	}
	return nil
}

// formatTemplate substitutes named fields with the given values. Escaped
// braces collapse to single braces. A named field with no supplied value is
// left as an empty string; validation has already guaranteed custom templates
// only reference known placeholders, and the built-in prompts always receive
// every value they use.
func formatTemplate(template string, values map[string]string) string {
	var b strings.Builder
	runes := []rune(template)
	i := 0
	n := len(runes)
	for i < n {
		c := runes[i]
		switch c {
		case '{':
			if i+1 < n && runes[i+1] == '{' {
				b.WriteByte('{')
				i += 2
				continue
			}
			j := i + 1
			for j < n && runes[j] != '}' {
				j++
			}
			if j >= n {
				// Malformed; emit verbatim rather than looping forever.
				b.WriteString(string(runes[i:]))
				return b.String()
			}
			field := string(runes[i+1 : j])
			name := field
			if idx := strings.IndexAny(name, "!:"); idx >= 0 {
				name = name[:idx]
			}
			b.WriteString(values[name])
			i = j + 1
		case '}':
			if i+1 < n && runes[i+1] == '}' {
				b.WriteByte('}')
				i += 2
				continue
			}
			b.WriteByte('}')
			i++
		default:
			b.WriteRune(c)
			i++
		}
	}
	return b.String()
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// formatList renders a slice the way Python prints a sorted list of strings,
// e.g. ['a', 'b'], so error messages read as they did under Python.
func formatList(items []string) string {
	quoted := make([]string, len(items))
	for i, it := range items {
		quoted[i] = "'" + it + "'"
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}
