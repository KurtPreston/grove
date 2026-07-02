package recipe

import (
	"os"
	"strings"
)

// ExpandString substitutes $VAR / ${VAR} in s using the given environment slice.
func ExpandString(s string, env []string) string {
	return os.Expand(s, envLookup(env))
}

func envLookup(env []string) func(string) string {
	m := make(map[string]string, len(env))
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok {
			m[k] = v
		}
	}
	return func(key string) string { return m[key] }
}

// ExpandValue recursively substitutes env vars in string leaves of v.
func ExpandValue(v any, env []string) any {
	switch x := v.(type) {
	case string:
		return ExpandString(x, env)
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			out[k] = ExpandValue(val, env)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, val := range x {
			out[i] = ExpandValue(val, env)
		}
		return out
	default:
		return v
	}
}
