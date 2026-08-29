package hop

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"example.com/ratw/internal/ring"
)

// deadlineHeader carries the caller's absolute deadline downstream so an inner
// timeout always fires before its caller's, and the failure is attributed to the
// region that actually stalled (Design §5).
const deadlineHeader = "X-Ratw-Deadline-Unix-Ms"

// reserve is the slack each hop keeps to write its own failure response after a
// downstream timeout, rather than being cut off mid-reply.
const reserve = 2 * time.Second

func (h *Handler) deadline(r *http.Request) (context.Context, context.CancelFunc) {
	max := time.Now().Add(time.Duration(h.cfg.DeadlineSec) * time.Second)
	if v := r.Header.Get(deadlineHeader); v != "" {
		if ms, err := strconv.ParseInt(v, 10, 64); err == nil {
			if inbound := time.UnixMilli(ms).Add(-reserve); inbound.Before(max) {
				max = inbound
			}
		}
	}
	return context.WithDeadline(r.Context(), max)
}

// forward sends the envelope to the next hop and BLOCKS until it replies. The reply
// is returned even on a non-200 so the partial chain propagates upward (FR-6.1).
func (h *Handler) forward(ctx context.Context, region, route string, e *ring.Envelope) (*ring.Envelope, error) {
	base, ok := h.cfg.Peers[region]
	if !ok {
		return nil, fmt.Errorf("no peer configured for region %q", region)
	}
	url := base + route

	body, err := json.Marshal(e)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if dl, ok := ctx.Deadline(); ok {
		req.Header.Set(deadlineHeader, strconv.FormatInt(dl.UnixMilli(), 10))
	}
	if err := h.authorize(ctx, req, base); err != nil {
		return nil, fmt.Errorf("minting id token for %s: %w", region, err)
	}

	h.log(logEntry{Message: "forwarding", TraceUUID: e.Core.TraceUUID,
		HopIndex: e.HopIndex, Stage: "forward", Object: url})

	resp, err := h.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("calling %s: %w", region, err)
	}
	defer resp.Body.Close()

	var reply ring.Envelope
	if err := json.NewDecoder(resp.Body).Decode(&reply); err != nil {
		return nil, fmt.Errorf("decoding reply from %s (status %d): %w", region, resp.StatusCode, err)
	}
	if resp.StatusCode != http.StatusOK {
		// Carry the partial chain up with the original failure intact.
		return &reply, fmt.Errorf("hop %s returned %d", region, resp.StatusCode)
	}
	return &reply, nil
}

func (h *Handler) client() *http.Client {
	return &http.Client{Timeout: 0} // the context governs; no competing timeout
}
