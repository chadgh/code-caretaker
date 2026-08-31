package steps

import "strconv"

// paramStr reads a string step param, returning def when absent or not a
// string.
func paramStr(params map[string]any, key, def string) string {
	if v, ok := params[key].(string); ok {
		return v
	}
	return def
}

// paramInt reads an integer step param, mirroring Python's int() coercion:
// native ints pass through, floats truncate, strings parse. The bool ok is
// false when the key is absent or uncoercible.
func paramInt(params map[string]any, key string) (int, bool) {
	v, ok := params[key]
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case int64:
		return int(n), true
	case int:
		return n, true
	case float64:
		return int(n), true
	case string:
		if parsed, err := strconv.Atoi(n); err == nil {
			return parsed, true
		}
	}
	return 0, false
}

// str returns a value as a string, or "" if absent or not a string.
func str(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// numToString renders a JSON-decoded number (float64) as an integer string,
// matching how Python printed the raw int from the API response.
func numToString(v any) string {
	switch n := v.(type) {
	case float64:
		return strconv.Itoa(int(n))
	case int:
		return strconv.Itoa(n)
	case int64:
		return strconv.Itoa(int(n))
	case string:
		return n
	default:
		return ""
	}
}
