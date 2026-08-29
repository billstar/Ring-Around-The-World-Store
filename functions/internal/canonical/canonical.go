// Package canonical produces a deterministic JSON encoding used for all hash-chain
// computation. Every hop, and the browser verifier, MUST agree byte-for-byte.
//
// The rules, kept deliberately small so they can be reimplemented in ~15 lines of JS:
//
//  1. Object keys sorted lexicographically (encoding/json does this for maps).
//  2. HTML escaping disabled (Go escapes <, >, & by default; JSON.stringify does not).
//  3. No trailing newline (encoding/json's Encoder appends one).
//  4. Number literals preserved exactly as written, never round-tripped through
//     float64. GCS generation numbers are ~1.7e15; a float64 round-trip renders them
//     in scientific notation and silently breaks the chain.
package canonical

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Marshal returns the canonical JSON encoding of v.
func Marshal(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("canonical: initial marshal: %w", err)
	}

	// Re-decode into generic containers so map-key sorting applies at every depth.
	// UseNumber keeps integers as literals rather than float64 (rule 4).
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var generic any
	if err := dec.Decode(&generic); err != nil {
		return nil, fmt.Errorf("canonical: re-decode: %w", err)
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(generic); err != nil {
		return nil, fmt.Errorf("canonical: re-encode: %w", err)
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}
