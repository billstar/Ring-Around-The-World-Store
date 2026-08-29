package ring

import (
	"fmt"
	"regexp"
	"slices"
	"time"
)

// Regions is the compile-time allowlist. A region name that is not in this map can
// never be dialed, and is never resolved to an address from request content (FR-2.1).
var Regions = map[string]string{
	"us-west1":        "Oregon",
	"us-central1":     "Iowa",
	"us-east4":        "N. Virginia",
	"europe-west1":    "Belgium",
	"europe-central2": "Warsaw",
	"asia-northeast1": "Tokyo",
	"asia-east1":      "Taiwan",
	"asia-southeast1": "Singapore",
}

// canonicalRing is the default sequence used when the client supplies none (FR-1.4).
// It is unexported and only ever handed out via DefaultSequence, which copies it.
// Handing out the slice itself would alias one shared backing array into every
// envelope in the process, where a single in-place mutation would silently change
// the default ring for all subsequent requests.
var canonicalRing = []string{
	"us-west1", "us-central1", "us-east4",
	"europe-west1", "europe-central2", "asia-northeast1",
}

// DefaultSequence returns a fresh copy of the canonical ring.
func DefaultSequence() []string { return slices.Clone(canonicalRing) }

const (
	MaxPayloadBytes = 4096
	MinSequence     = 2
	MaxSequence     = 8
)

// IsUUID reports whether s is a lowercase v4-shaped uuid. Used for the trace id and
// for the client id, both of which reach a Cloud Logging filter.
func IsUUID(s string) bool { return uuidRe.MatchString(s) }

var uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// Core is immutable for the life of a ring. The genesis hash covers exactly this.
type Core struct {
	TraceUUID string   `json:"trace_uuid"`
	Payload   string   `json:"payload"`
	Sequence  []string `json:"sequence"`
	CreatedAt string   `json:"created_at"`
}

// Receipt is one hop's proof of custody. LinkHash is omitted when hashing the body.
type Receipt struct {
	HopIndex        int    `json:"hop_index"`
	Region          string `json:"region"`
	Location        string `json:"location"`
	Bucket          string `json:"bucket"`
	Object          string `json:"object"`
	Generation      int64  `json:"generation"`
	CRC32C          string `json:"crc32c"`
	PayloadSHA256   string `json:"payload_sha256"`
	VerifiedReadback bool  `json:"verified_readback"`
	RingClose       bool   `json:"ring_close"`

	// Wall clock: ±few ms of cross-region skew (Google NTP, leap-smeared).
	// Good enough to order hops; not a bounded-uncertainty interval.
	TReceived  string `json:"t_received"`
	TWritten   string `json:"t_written"`
	TReadback  string `json:"t_readback"`
	TForwarded string `json:"t_forwarded"`

	// Monotonic and exact: measured on one machine with one clock.
	DWriteUS int64 `json:"d_write_us"`
	DReadUS  int64 `json:"d_read_us"`
	DTotalUS int64 `json:"d_total_us"`

	PrevLinkHash string `json:"prev_link_hash"`
	LinkHash     string `json:"link_hash,omitempty"` // omitempty: excluded from its own hash
}

// Failure is set once by the hop that fails, and passed upward unmodified (FR-6.2).
type Failure struct {
	Region   string `json:"region"`
	HopIndex int    `json:"hop_index"`
	Stage    string `json:"stage"`
	Error    string `json:"error"`
	At       string `json:"at"`
}

type Envelope struct {
	Version   int       `json:"version"`
	Core      Core      `json:"core"`
	HopIndex  int       `json:"hop_index"`
	RingClose bool      `json:"ring_close"`
	Receipts  []Receipt `json:"receipts"`
	Failure   *Failure  `json:"failure"`
}

func Now() string { return time.Now().UTC().Format("2006-01-02T15:04:05.000Z") }

// Validate enforces FR-2 in full. Every hop runs this, not just the ingress.
func (e *Envelope) Validate() error {
	if e.Version != 1 {
		return fmt.Errorf("unsupported envelope version %d", e.Version)
	}
	if !uuidRe.MatchString(e.Core.TraceUUID) {
		return fmt.Errorf("trace_uuid is not a lowercase uuid")
	}
	if len(e.Core.Payload) > MaxPayloadBytes {
		return fmt.Errorf("payload %d bytes exceeds %d", len(e.Core.Payload), MaxPayloadBytes)
	}
	n := len(e.Core.Sequence)
	if n < MinSequence || n > MaxSequence {
		return fmt.Errorf("sequence length %d outside [%d,%d]", n, MinSequence, MaxSequence)
	}
	seen := map[string]bool{}
	for _, r := range e.Core.Sequence {
		if _, ok := Regions[r]; !ok {
			return fmt.Errorf("region %q is not in the allowlist", r)
		}
		if seen[r] {
			return fmt.Errorf("region %q appears more than once", r)
		}
		seen[r] = true
	}
	// FR-2.4: hop_index strictly increases; it must equal the receipts already collected.
	if e.HopIndex != len(e.Receipts) {
		return fmt.Errorf("hop_index %d does not match %d receipts", e.HopIndex, len(e.Receipts))
	}
	// A ring close is the sole permitted revisit, and only as the final hop (FR-2.5).
	if e.RingClose && e.HopIndex != n {
		return fmt.Errorf("ring_close at hop %d, expected %d", e.HopIndex, n)
	}
	if !e.RingClose && e.HopIndex >= n {
		return fmt.Errorf("hop_index %d past end of sequence without ring_close", e.HopIndex)
	}
	return nil
}

// ExpectedRegion is the region that should be handling this envelope right now.
func (e *Envelope) ExpectedRegion() string {
	if e.RingClose {
		return e.Core.Sequence[0]
	}
	return e.Core.Sequence[e.HopIndex]
}

// Next reports where this envelope goes next. It is called AFTER the current hop has
// appended its receipt, so HopIndex already names the next position in the sequence.
// ok is false when the current hop is terminal (the ring close).
func (e *Envelope) Next() (region, route string, ok bool) {
	if e.RingClose {
		return "", "", false
	}
	if e.HopIndex < len(e.Core.Sequence) {
		return e.Core.Sequence[e.HopIndex], "/hop", true
	}
	return e.Core.Sequence[0], "/close", true // close the ring back at the origin
}

func (e *Envelope) Fail(region, stage string, err error) {
	if e.Failure != nil {
		return // first failure wins; upstream hops must not overwrite it (FR-6.2)
	}
	e.Failure = &Failure{
		Region: region, HopIndex: e.HopIndex, Stage: stage,
		Error: err.Error(), At: Now(),
	}
}
