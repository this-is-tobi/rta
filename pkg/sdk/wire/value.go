// Package wire converts between the Go capability contract (pkg/plugin,
// pkg/view) and its wire form (proto/rta/v1).
//
// Both directions live here, and deliberately in one package rather than one
// per side. A host decoding what a plugin encoded is the single place this
// system can silently lose meaning — a field the encoder writes and the
// decoder ignores looks exactly like a plugin that did not set it — so the
// two halves are written together, tested against each other by round trip,
// and drift is a failing test rather than a support thread.
//
// It is public because both sides need it: pkg/sdk serves a Go plugin over
// gRPC, and the host consumes one. A plugin author normally never names this
// package.
package wire

import (
	rtav1 "github.com/this-is-tobi/rule-them-all/proto/rta/v1"
)

// ValueToProto encodes one request value, declared default, or bound.
//
// nil encodes as nil, which is the wire's way of saying "not given" — the
// distinction the oneof exists for. A caller who passed --limit 0 and a
// caller who passed no --limit at all are asking different questions, and a
// contract that flattens them makes every numeric default unexpressible.
//
// An unknown type encodes as nil rather than as its Go formatting. Something
// no Field.Type can describe has no business crossing: rendering it as a
// string would hand the handler a value its declared type says is impossible,
// which is the failure mode Field.Type was closed to prevent (ADR 0011).
func ValueToProto(v any) *rtav1.Value {
	switch n := v.(type) {
	case nil:
		return nil
	case string:
		return &rtav1.Value{Kind: &rtav1.Value_StringValue{StringValue: n}}
	case bool:
		return &rtav1.Value{Kind: &rtav1.Value_BoolValue{BoolValue: n}}
	case int:
		return intValue(int64(n))
	case int8:
		return intValue(int64(n))
	case int16:
		return intValue(int64(n))
	case int32:
		return intValue(int64(n))
	case int64:
		return intValue(n)
	case uint:
		return intValue(int64(n))
	case uint8:
		return intValue(int64(n))
	case uint16:
		return intValue(int64(n))
	case uint32:
		return intValue(int64(n))
	case uint64:
		return intValue(int64(n))
	case float32:
		return floatValue(float64(n))
	case float64:
		return floatValue(n)
	case []string:
		return &rtav1.Value{Kind: &rtav1.Value_StringListValue{
			StringListValue: &rtav1.StringList{Values: n},
		}}
	case []any:
		// What an MCP client sends for a string-slice input, since JSON has
		// no typed arrays. internal/mcp already accepts it and pkg/plugin's
		// StringSlice accessor already reads it, so refusing it here would
		// make a plugin stricter than a built-in for the same declaration.
		out := make([]string, 0, len(n))
		for _, e := range n {
			s, ok := e.(string)
			if !ok {
				return nil
			}
			out = append(out, s)
		}
		return &rtav1.Value{Kind: &rtav1.Value_StringListValue{
			StringListValue: &rtav1.StringList{Values: out},
		}}
	}
	return nil
}

func intValue(n int64) *rtav1.Value {
	return &rtav1.Value{Kind: &rtav1.Value_IntValue{IntValue: n}}
}

func floatValue(f float64) *rtav1.Value {
	return &rtav1.Value{Kind: &rtav1.Value_FloatValue{FloatValue: f}}
}

// ValueFromProto decodes one value.
//
// Integer width is not preserved: every Go integer type encodes to the wire's
// one integer and comes back as int64. That is invisible to a handler —
// Request.Int, Request.Float and Resolve all read every width — and the
// alternative is a wire with nine integer types to keep a distinction nobody
// can observe.
func ValueFromProto(v *rtav1.Value) any {
	if v == nil {
		return nil
	}
	switch k := v.Kind.(type) {
	case *rtav1.Value_StringValue:
		return k.StringValue
	case *rtav1.Value_IntValue:
		return k.IntValue
	case *rtav1.Value_FloatValue:
		return k.FloatValue
	case *rtav1.Value_BoolValue:
		return k.BoolValue
	case *rtav1.Value_StringListValue:
		if k.StringListValue == nil {
			return []string(nil)
		}
		return k.StringListValue.Values
	}
	return nil
}

// ValuesToProto encodes a request's value map.
func ValuesToProto(in map[string]any) map[string]*rtav1.Value {
	if in == nil {
		return nil
	}
	out := make(map[string]*rtav1.Value, len(in))
	for k, v := range in {
		// A nil entry is dropped rather than encoded as an unset oneof: the
		// map already expresses absence by not having the key, and two ways
		// to say "not given" is one more than any decoder will handle
		// consistently.
		if pv := ValueToProto(v); pv != nil {
			out[k] = pv
		}
	}
	return out
}

// ValuesFromProto decodes a request's value map.
func ValuesFromProto(in map[string]*rtav1.Value) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		if dv := ValueFromProto(v); dv != nil {
			out[k] = dv
		}
	}
	return out
}

// mapSlice converts a list, preserving nil as nil.
//
// proto3 has no way for a repeated field to say "empty but present": an empty
// list and an absent one are the same bytes. So nil is the canonical form for
// both, and every converter here has to agree on that or a round trip through
// an actual encode/decode disagrees with a round trip through these functions
// alone — which is the worst kind of test, one that passes on the thing it can
// see and lies about the thing it cannot.
func mapSlice[A, B any](in []A, f func(A) B) []B {
	if len(in) == 0 {
		return nil
	}
	out := make([]B, len(in))
	for i, v := range in {
		out[i] = f(v)
	}
	return out
}
