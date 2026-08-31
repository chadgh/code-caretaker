package gh

import "strconv"

// PR maps come from `gh --json` output decoded into map[string]any, so numbers
// arrive as float64 and everything else as string / []any. These helpers read
// fields back out with the right types.

// str returns a value as a string, or "" if it is absent or not a string.
func str(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func itoa(n int) string { return strconv.Itoa(n) }

// prNumber returns a PR's number as an int.
func prNumber(pr map[string]any) int { return Number(pr) }

// prNumberString returns a PR's number rendered as a string.
func prNumberString(pr map[string]any) string { return itoa(Number(pr)) }

// Number returns a PR's number as an int (JSON decodes it as float64).
func Number(pr map[string]any) int {
	switch v := pr["number"].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case int64:
		return int(v)
	default:
		return 0
	}
}

// String returns a string-valued PR field, or "" when absent.
func String(pr map[string]any, key string) string {
	return str(pr[key])
}

// Rollup returns a PR's statusCheckRollup as a slice of maps.
func Rollup(pr map[string]any) []map[string]any {
	raw, ok := pr["statusCheckRollup"].([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		if m, ok := item.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}
