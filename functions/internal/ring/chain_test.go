package ring

import (
	"strings"
	"testing"

	"example.com/ratw/internal/canonical"
)

func core() Core {
	return Core{
		TraceUUID: "8f14e45f-ceea-467a-9c3b-1f2a4d5e6b70",
		Payload:   "hello world",
		Sequence:  DefaultSequence(),
		CreatedAt: "2026-08-29T19:04:11.221Z",
	}
}

func env() *Envelope {
	return &Envelope{Version: 1, Core: core(), Receipts: []Receipt{}}
}

func rcpt(region string) Receipt {
	return Receipt{
		Region: region, Location: Regions[region],
		Bucket: "ratw-" + region, Object: "traces/x/hop.json",
		Generation: 1724951051221334, CRC32C: "z8SuHQ==",
		PayloadSHA256: "9b74c9897b", VerifiedReadback: true,
		TReceived: "2026-08-29T19:04:11.402Z", TWritten: "2026-08-29T19:04:11.509Z",
		TReadback: "2026-08-29T19:04:11.548Z", TForwarded: "2026-08-29T19:04:11.551Z",
		DWriteUS: 107000, DReadUS: 39000, DTotalUS: 149000,
	}
}

// buildRing appends a full seven-receipt chain: six hops plus the ring close.
func buildRing(t *testing.T) *Envelope {
	t.Helper()
	e := env()
	for i, r := range DefaultSequence() {
		rc := rcpt(r)
		rc.HopIndex = i
		if err := e.Append(rc); err != nil {
			t.Fatalf("append %s: %v", r, err)
		}
	}
	closing := rcpt(DefaultSequence()[0])
	closing.HopIndex = len(DefaultSequence())
	closing.RingClose = true
	if err := e.Append(closing); err != nil {
		t.Fatalf("append close: %v", err)
	}
	return e
}

func TestRingVerifies(t *testing.T) {
	e := buildRing(t)
	if len(e.Receipts) != 7 {
		t.Fatalf("want 7 receipts across 6 regions, got %d", len(e.Receipts))
	}
	if err := e.Verify(); err != nil {
		t.Fatalf("freshly built ring must verify: %v", err)
	}
}

// The whole point of the project: any alteration anywhere must be detected.
func TestTamperDetection(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Envelope)
	}{
		{"payload altered", func(e *Envelope) { e.Core.Payload = "goodbye world" }},
		{"trace uuid altered", func(e *Envelope) { e.Core.TraceUUID = "00000000-0000-4000-8000-000000000000" }},
		{"sequence reordered", func(e *Envelope) {
			e.Core.Sequence[1], e.Core.Sequence[2] = e.Core.Sequence[2], e.Core.Sequence[1]
		}},
		{"region swapped mid-chain", func(e *Envelope) { e.Receipts[3].Region = "us-east4" }},
		{"generation number altered", func(e *Envelope) { e.Receipts[2].Generation = 1 }},
		{"crc altered", func(e *Envelope) { e.Receipts[4].CRC32C = "AAAAAA==" }},
		{"timing forged", func(e *Envelope) { e.Receipts[5].DTotalUS = 1 }},
		{"readback flag flipped", func(e *Envelope) { e.Receipts[1].VerifiedReadback = false }},
		{"receipt deleted", func(e *Envelope) { e.Receipts = append(e.Receipts[:3], e.Receipts[4:]...) }},
		{"receipts reordered", func(e *Envelope) {
			e.Receipts[2], e.Receipts[3] = e.Receipts[3], e.Receipts[2]
		}},
		{"link hash overwritten", func(e *Envelope) { e.Receipts[2].LinkHash = e.Receipts[3].LinkHash }},
		{"prev link rewritten", func(e *Envelope) { e.Receipts[4].PrevLinkHash = e.Receipts[0].LinkHash }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := buildRing(t)
			tc.mutate(e)
			if err := e.Verify(); err == nil {
				t.Fatalf("tampering went undetected: %s", tc.name)
			}
		})
	}
}

// A forger who rewrites a receipt AND recomputes every downstream link produces an
// internally consistent chain. This is expected, and is exactly why receipts are also
// anchored in seven GCS objects across six regions (FR-4.5). Documented as a test so
// nobody later mistakes the hash chain alone for the whole proof.
func TestFullRecomputationIsConsistent_AnchoringIsTheRealProof(t *testing.T) {
	e := buildRing(t)
	forged := env()
	for i, r := range e.Receipts {
		r.LinkHash, r.PrevLinkHash = "", ""
		if i == 3 {
			r.Region = "us-east4" // the lie
		}
		if err := forged.Append(r); err != nil {
			t.Fatal(err)
		}
	}
	if err := forged.Verify(); err != nil {
		t.Fatalf("a fully recomputed chain is self-consistent by construction: %v", err)
	}
	if forged.Receipts[6].LinkHash == e.Receipts[6].LinkHash {
		t.Fatal("forged chain must at least produce a different head hash")
	}
}

func TestCanonicalIsDeterministicAndJSCompatible(t *testing.T) {
	// Rule 4: large integers must not round-trip through float64. A GCS generation
	// number is ~1.7e15 and would render as 1.724951051221334e+15.
	b, err := canonical.Marshal(map[string]any{"generation": int64(1724951051221334)})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(b), `{"generation":1724951051221334}`; got != want {
		t.Fatalf("generation number mangled:\n got %s\nwant %s", got, want)
	}

	// Rule 2: Go escapes <, >, & by default; JSON.stringify does not. If this regresses,
	// any payload containing HTML breaks the browser verifier and nothing else.
	b, err = canonical.Marshal(map[string]any{"payload": "<b>a&b</b>"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(b), `{"payload":"<b>a&b</b>"}`; got != want {
		t.Fatalf("HTML escaping not disabled:\n got %s\nwant %s", got, want)
	}

	// Rule 1: key order is sorted, not insertion order.
	b, _ = canonical.Marshal(map[string]any{"z": 1, "a": 2, "m": 3})
	if got, want := string(b), `{"a":2,"m":3,"z":1}`; got != want {
		t.Fatalf("keys not sorted:\n got %s\nwant %s", got, want)
	}

	// Rule 3: no trailing newline.
	if strings.HasSuffix(string(b), "\n") {
		t.Fatal("canonical form must not end in a newline")
	}
}

// An empty link_hash must be omitted entirely from the canonical body, otherwise a
// receipt would be hashed with a field that is absent at verification time.
func TestLinkHashOmittedWhenEmpty(t *testing.T) {
	b, err := canonical.Marshal(rcpt("us-west1"))
	if err != nil {
		t.Fatal(err)
	}
	stripped := strings.ReplaceAll(string(b), `"prev_link_hash"`, `"_"`)
	if strings.Contains(stripped, "link_hash") {
		t.Fatalf("empty link_hash must be omitted: %s", b)
	}
}

// Golden vector. The browser verifier must reproduce this exact digest; if the JS and
// Go canonical forms ever diverge, this is the value to bisect against.
func TestGenesisGoldenVector(t *testing.T) {
	got, err := Genesis(core())
	if err != nil {
		t.Fatal(err)
	}
	// Pinned. The Python and browser verifiers must reproduce this exact digest; if
	// the implementations ever diverge, this is the value to bisect against.
	const want = "55fc7498426a6500068d2fa6f43ac311c7dcfff4ec43e5b1940ebd74de0f0049"
	if got != want {
		t.Fatalf("genesis golden vector changed:\n got %s\nwant %s", got, want)
	}
	if again, _ := Genesis(core()); got != again {
		t.Fatal("genesis is not deterministic")
	}
}

// DefaultSequence must hand out a copy: an envelope that mutates its own sequence
// must not change the default ring for every later request in the process.
func TestDefaultSequenceIsNotAliased(t *testing.T) {
	a := DefaultSequence()
	a[0], a[1] = a[1], a[0]
	b := DefaultSequence()
	if b[0] != "us-west1" || b[1] != "us-central1" {
		t.Fatalf("mutating one caller's sequence corrupted the shared default: %v", b)
	}
}

func TestValidateRejectsBadSequences(t *testing.T) {
	cases := []struct {
		name string
		fn   func(*Envelope)
	}{
		{"unknown region", func(e *Envelope) { e.Core.Sequence = []string{"us-west1", "mars-north1"} }},
		{"url as region", func(e *Envelope) {
			e.Core.Sequence = []string{"us-west1", "https://evil.example.com/"}
		}},
		{"duplicate region", func(e *Envelope) { e.Core.Sequence = []string{"us-west1", "us-west1"} }},
		{"too short", func(e *Envelope) { e.Core.Sequence = []string{"us-west1"} }},
		{"too long", func(e *Envelope) {
			e.Core.Sequence = []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"}
		}},
		{"oversized payload", func(e *Envelope) { e.Core.Payload = strings.Repeat("x", MaxPayloadBytes+1) }},
		{"bad uuid", func(e *Envelope) { e.Core.TraceUUID = "not-a-uuid" }},
		{"hop index ahead of receipts", func(e *Envelope) { e.HopIndex = 3 }},
		{"ring close too early", func(e *Envelope) { e.RingClose = true }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := env()
			tc.fn(e)
			if err := e.Validate(); err == nil {
				t.Fatalf("validation must reject: %s", tc.name)
			}
		})
	}
	if err := env().Validate(); err != nil {
		t.Fatalf("the canonical ring must validate: %v", err)
	}
}
