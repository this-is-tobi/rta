package plugin

import "encoding/json"

// Resolve turns the values a surface collected into the values a handler
// actually runs with: declared defaults filled in, numbers normalised to one
// Go type, and declared bounds applied.
//
// It exists because four surfaces build a Request and each was doing a
// different subset of that work. The CLI got it right by accident — cobra
// bakes defaults into the flag set, so collectValues reads them back — while
// the TUI and the dashboard filled defaults only when the caller supplied no
// values at all, so a tile pinned as
//
//	{id: sys.ps, with: {limit: 5}}
//
// dropped every *other* declared default on the floor and handed the handler
// zero values it had no way to distinguish from real ones. The same config
// worked from a shell, which is the worst version of a bug: the user's file
// is right, the capability is right, and only one surface is wrong.
//
// Normalising types is the second half of the same problem. The config loader
// decodes untyped non-negative YAML integers as uint64, which Request.Int did
// not recognise, so it returned 0 — again silently, and again only on the
// surface that reads config.
//
// Every surface that runs a handler calls this. Nothing downstream has to
// know which of the values were declared, defaulted, or clamped.
func Resolve(c Capability, values map[string]any) map[string]any {
	out := make(map[string]any, len(c.Inputs)+len(values))
	for _, f := range c.Inputs {
		if f.Default != nil {
			out[f.Name] = f.Default
		}
	}
	for k, v := range values {
		out[k] = v
	}

	byName := make(map[string]Field, len(c.Inputs))
	for _, f := range c.Inputs {
		byName[f.Name] = f
	}
	for name, v := range out {
		f, declared := byName[name]
		if !declared {
			continue
		}
		switch f.Type {
		case Int:
			if n, ok := toInt(v); ok {
				out[name] = clampInt(n, f)
			}
		case Float:
			if n, ok := toFloat(v); ok {
				out[name] = clampFloat(n, f)
			}
		}
	}
	return out
}

// toInt accepts every shape an integer arrives in. YAML gives uint64, JSON
// gives float64, cobra gives int, and a plugin's own Default is whatever its
// author wrote — so the set is wider than it looks, and a value this does not
// recognise is left alone rather than replaced with a confident zero.
func toInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int8:
		return int(n), true
	case int16:
		return int(n), true
	case int32:
		return int(n), true
	case int64:
		return int(n), true
	case uint:
		return int(n), true
	case uint8:
		return int(n), true
	case uint16:
		return int(n), true
	case uint32:
		return int(n), true
	case uint64:
		return int(n), true
	case float32:
		return int(n), true
	case float64:
		return int(n), true
	case json.Number:
		if i, err := n.Int64(); err == nil {
			return int(i), true
		}
	}
	return 0, false
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case json.Number:
		if f, err := n.Float64(); err == nil {
			return f, true
		}
	default:
		if i, ok := toInt(v); ok {
			return float64(i), true
		}
	}
	return 0, false
}

func clampInt(n int, f Field) int {
	if lo, ok := toInt(f.Min); ok && n < lo {
		n = lo
	}
	if hi, ok := toInt(f.Max); ok && n > hi {
		n = hi
	}
	return n
}

func clampFloat(n float64, f Field) float64 {
	if lo, ok := toFloat(f.Min); ok && n < lo {
		n = lo
	}
	if hi, ok := toFloat(f.Max); ok && n > hi {
		n = hi
	}
	return n
}
