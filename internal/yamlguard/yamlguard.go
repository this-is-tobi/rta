// Package yamlguard refuses a YAML document that would expand during decode
// into far more than it costs to store.
//
// Three files rta reads are written by somebody other than the person running
// it, and every one of them is decoded before anything about it is trusted:
// the config file (shared between operators and machines), the team policy
// file (found by walking up to 64 parent directories, so a cloned repository
// can ship one), and a plugin index's manifests (cloned from a repository
// somebody else merges to, and re-read on every search). A guard that lived in
// only one of them was the actual state of this codebase until a scan found
// the other two.
package yamlguard

import (
	"errors"

	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"
)

// ErrAnchors is what RefuseAnchors returns. Callers wrap it in their own
// *view.Error so the message names the file the reader can actually go and
// fix — a policy file and a manifest are read by different people.
var ErrAnchors = errors.New(
	"uses a YAML anchor or alias (&name / *name), which this file does not support")

// RefuseAnchors rejects a document that uses a YAML anchor or alias
// (&name / *name) before a decoder ever gets to substitute one.
//
// None of the files this guards has a legitimate use for either: they are
// flat, known sets of fields, none of which benefit from YAML's own
// value-reuse syntax. What anchors enable instead is a "billion laughs" bomb —
// a handful of nested anchor/alias pairs, each fanning out 10x into the next,
// expands a few hundred bytes of syntactically valid YAML into 10^9+ decoded
// values, exhausting memory. Measured against this repository's own Ceiling
// struct rather than assumed: 510 bytes of policy file cost 37.8 seconds and
// 34.7 GB of allocation before the decoder gave up on a type mismatch. The
// type error arrives *after* the memory is committed, so a mismatched shape is
// not a defense.
//
// Rather than pick a "safe" nesting depth or alias count a cleverer bomb could
// still clear, this refuses the syntax outright — checked by parsing alone,
// never decoding: goccy's parser builds a graph proportional to the bytes on
// disk (an AliasNode holds the name it references, not a dereferenced copy of
// the anchor's subtree), so walking that graph costs exactly what parsing it
// already did, before anything is substituted.
//
// Two properties a caller depends on. It must run before the decode, not
// beside it — the expansion is the decode. And it is not a substitute for a
// size cap: 510 bytes was enough, so a cap on the file bounds the parse and
// this bounds the expansion, and the two are not interchangeable.
func RefuseAnchors(data []byte) error {
	file, err := parser.ParseBytes(data, 0)
	if err != nil {
		// The decoder will hit and report the identical parse failure with
		// its own message, which names the line; this just isn't it.
		return nil
	}
	v := &anchorVisitor{}
	for _, doc := range file.Docs {
		ast.Walk(v, doc)
		if v.found {
			// The matched node is deliberately not echoed into the message:
			// an AnchorNode's own String() walks the value it anchors, and
			// while that is bounded by what is actually written in the file
			// (never a dereferenced, expanded copy), there is no reason to
			// stringify attacker-controlled YAML into an error message when
			// naming the rule is enough.
			return ErrAnchors
		}
	}
	return nil
}

type anchorVisitor struct{ found bool }

func (v *anchorVisitor) Visit(n ast.Node) ast.Visitor {
	if v.found {
		return nil
	}
	switch n.(type) {
	case *ast.AnchorNode, *ast.AliasNode:
		v.found = true
		return nil
	}
	return v
}
