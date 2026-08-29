package ring

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"ratw/internal/canonical"
)

// The chain:
//
//	genesis = SHA256(canonical(core))
//	link_0  = SHA256(genesis || canonical(receipt_0_body))
//	link_n  = SHA256(link_n-1 || canonical(receipt_n_body))
//
// Concatenation is over the 32 RAW bytes of the previous digest followed by the
// canonical body bytes — never hex strings — so there is no ambiguity about
// encoding at the join.

func Genesis(c Core) (string, error) {
	b, err := canonical.Marshal(c)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// linkFor computes a receipt's link hash given the previous link (hex).
// The receipt's own LinkHash is cleared first so it is excluded from its own digest
// (the field is `omitempty`, so it vanishes from the canonical form entirely).
func linkFor(prevHex string, r Receipt) (string, error) {
	prev, err := hex.DecodeString(prevHex)
	if err != nil || len(prev) != sha256.Size {
		return "", fmt.Errorf("previous link hash is not a 32-byte hex digest")
	}
	r.LinkHash = ""
	body, err := canonical.Marshal(r)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	h.Write(prev)
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Append links a receipt onto the envelope's chain and appends it.
func (e *Envelope) Append(r Receipt) error {
	prev, err := e.head()
	if err != nil {
		return err
	}
	r.PrevLinkHash = prev
	link, err := linkFor(prev, r)
	if err != nil {
		return err
	}
	r.LinkHash = link
	e.Receipts = append(e.Receipts, r)
	e.HopIndex = len(e.Receipts)
	return nil
}

// head is the hash the next receipt links onto: the genesis, or the last link.
func (e *Envelope) head() (string, error) {
	if len(e.Receipts) == 0 {
		return Genesis(e.Core)
	}
	return e.Receipts[len(e.Receipts)-1].LinkHash, nil
}

// Verify replays the entire chain from the genesis. Every hop runs this on the
// inbound envelope BEFORE doing its own work; a broken link is a hard failure (FR-4.3).
func (e *Envelope) Verify() error {
	prev, err := Genesis(e.Core)
	if err != nil {
		return err
	}
	for i, r := range e.Receipts {
		if r.HopIndex != i {
			return fmt.Errorf("receipt %d claims hop_index %d", i, r.HopIndex)
		}
		if r.PrevLinkHash != prev {
			return fmt.Errorf("receipt %d (%s): prev_link_hash does not match the chain", i, r.Region)
		}
		want, err := linkFor(prev, r)
		if err != nil {
			return fmt.Errorf("receipt %d (%s): %w", i, r.Region, err)
		}
		if want != r.LinkHash {
			return fmt.Errorf("receipt %d (%s): link_hash mismatch — receipt was altered", i, r.Region)
		}
		prev = r.LinkHash
	}
	return nil
}
