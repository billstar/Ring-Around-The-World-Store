package hop

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"example.com/ratw/internal/canonical"
	"example.com/ratw/internal/ring"
	"example.com/ratw/internal/store"
)

type Handler struct {
	cfg     Config
	st      store.Store
	project string
	mux     *http.ServeMux
}

func New(cfg Config, st store.Store) *Handler {
	h := &Handler{cfg: cfg, st: st, project: os.Getenv("GOOGLE_CLOUD_PROJECT"), mux: http.NewServeMux()}
	if cfg.IsOrigin {
		h.mux.HandleFunc("/ring", h.handleRing)   // public ingress
		h.mux.HandleFunc("/close", h.handleClose) // OIDC-verified in-handler (Design §6)
	}
	h.mux.HandleFunc("/hop", h.handleHop)
	h.mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, "ok %s\n", h.cfg.Region)
	})
	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) { h.mux.ServeHTTP(w, r) }

func objectName(uuid string, hopIndex int, region string) string {
	return fmt.Sprintf("traces/%s/hop-%02d-%s.json", uuid, hopIndex, region)
}

// process performs this hop's write → readback → receipt → log cycle (FR-3).
// On any failure it stamps the envelope's failure field and returns the error.
func (h *Handler) process(ctx context.Context, e *ring.Envelope) error {
	start := time.Now()
	tReceived := ring.Now()
	idx := e.HopIndex
	name := objectName(e.Core.TraceUUID, idx, h.cfg.Region)

	h.log(logEntry{Message: "hop received", TraceUUID: e.Core.TraceUUID, HopIndex: idx, Stage: "received"})

	// The object records the envelope exactly as it ARRIVED: proof that the chain up
	// to this point reached this region intact.
	body, err := canonical.Marshal(e)
	if err != nil {
		e.Fail(h.cfg.Region, "marshal", err)
		return err
	}

	wStart := time.Now()
	obj, err := h.st.Write(ctx, name, body)
	dWrite := time.Since(wStart)
	if err != nil {
		e.Fail(h.cfg.Region, "write", err)
		h.log(logEntry{Severity: "ERROR", Message: "write failed", TraceUUID: e.Core.TraceUUID,
			HopIndex: idx, Stage: "write", Object: name, Error: err.Error()})
		return err
	}
	tWritten := ring.Now()

	// Read back and compare byte-for-byte. Strong consistency makes this cheap, but
	// the point is the receipt: an independently checkable generation + CRC32C.
	rStart := time.Now()
	got, readObj, err := h.st.Read(ctx, name)
	dRead := time.Since(rStart)
	if err != nil {
		e.Fail(h.cfg.Region, "readback", err)
		h.log(logEntry{Severity: "ERROR", Message: "readback failed", TraceUUID: e.Core.TraceUUID,
			HopIndex: idx, Stage: "readback", Object: name, Error: err.Error()})
		return err
	}
	if !bytes.Equal(body, got) {
		err := fmt.Errorf("readback mismatch: wrote %d bytes, read %d", len(body), len(got))
		e.Fail(h.cfg.Region, "readback", err)
		return err
	}
	if readObj.CRC32C != obj.CRC32C {
		err := fmt.Errorf("crc32c mismatch: wrote %s, read %s", obj.CRC32C, readObj.CRC32C)
		e.Fail(h.cfg.Region, "readback", err)
		return err
	}
	tReadback := ring.Now()

	sum := sha256.Sum256([]byte(e.Core.Payload))
	rc := ring.Receipt{
		HopIndex: idx, Region: h.cfg.Region, Location: ring.Regions[h.cfg.Region],
		Bucket: h.st.Bucket(), Object: name,
		Generation: obj.Generation, CRC32C: obj.CRC32C,
		PayloadSHA256: hex.EncodeToString(sum[:]), VerifiedReadback: true,
		RingClose: e.RingClose,
		TReceived: tReceived, TWritten: tWritten, TReadback: tReadback, TForwarded: ring.Now(),
		DWriteUS: dWrite.Microseconds(), DReadUS: dRead.Microseconds(),
		DTotalUS: time.Since(start).Microseconds(),
	}
	if err := e.Append(rc); err != nil {
		e.Fail(h.cfg.Region, "chain-append", err)
		return err
	}

	h.log(logEntry{Message: "receipt written", TraceUUID: e.Core.TraceUUID, HopIndex: idx,
		Stage: "receipt", Object: name, Generation: obj.Generation,
		DurationMS: time.Since(start).Milliseconds(),
		LinkHash:   e.Receipts[len(e.Receipts)-1].LinkHash})
	return nil
}

// admit runs the checks every hop performs on an inbound envelope, in order.
func (h *Handler) admit(e *ring.Envelope) error {
	if err := e.Validate(); err != nil {
		return fmt.Errorf("validation: %w", err)
	}
	if err := e.Verify(); err != nil {
		return fmt.Errorf("chain verification: %w", err)
	}
	if want := e.ExpectedRegion(); want != h.cfg.Region {
		return fmt.Errorf("envelope routed to %s but expects %s", h.cfg.Region, want)
	}
	return nil
}

// relay is the shared body of /hop and /close: admit, process, forward if not terminal.
func (h *Handler) relay(ctx context.Context, e *ring.Envelope) error {
	if err := h.admit(e); err != nil {
		e.Fail(h.cfg.Region, "admission", err)
		return err
	}
	if err := h.process(ctx, e); err != nil {
		return err
	}
	region, route, ok := e.Next()
	if !ok {
		return nil // ring close: terminal, returns immediately (Design §1.1)
	}
	if route == "/close" {
		e.RingClose = true // the sole permitted revisit of an already-visited region
	}
	reply, err := h.forward(ctx, region, route, e)
	if err != nil {
		// Preserve the downstream failure verbatim rather than masking it (FR-6.2).
		if reply != nil {
			*e = *reply
		} else {
			e.Fail(h.cfg.Region, "forward", err)
		}
		return err
	}
	*e = *reply
	return nil
}

func (h *Handler) handleHop(w http.ResponseWriter, r *http.Request) {
	e, ok := decode(w, r)
	if !ok {
		return
	}
	ctx, cancel := h.deadline(r)
	defer cancel()
	if err := h.relay(ctx, e); err != nil {
		writeJSON(w, http.StatusBadGateway, e)
		return
	}
	writeJSON(w, http.StatusOK, e)
}

func (h *Handler) handleClose(w http.ResponseWriter, r *http.Request) {
	if err := h.verifyCaller(r); err != nil {
		http.Error(w, "unauthorized: "+err.Error(), http.StatusUnauthorized)
		return
	}
	e, ok := decode(w, r)
	if !ok {
		return
	}
	if !e.RingClose {
		http.Error(w, "/close requires ring_close", http.StatusBadRequest)
		return
	}
	ctx, cancel := h.deadline(r)
	defer cancel()
	if err := h.relay(ctx, e); err != nil {
		writeJSON(w, http.StatusBadGateway, e)
		return
	}
	h.log(logEntry{Message: "ring closed", TraceUUID: e.Core.TraceUUID,
		HopIndex: e.HopIndex, Stage: "ring-close"})
	writeJSON(w, http.StatusOK, e)
}

type clientRequest struct {
	TraceUUID string   `json:"trace_uuid"`
	Payload   string   `json:"payload"`
	Sequence  []string `json:"sequence"`
}

func (h *Handler) handleRing(w http.ResponseWriter, r *http.Request) {
	h.cors(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var req clientRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		http.Error(w, "bad request body", http.StatusBadRequest)
		return
	}
	seq := req.Sequence
	if len(seq) == 0 {
		seq = h.cfg.Ring // what is actually deployed
	}
	if len(seq) == 0 {
		seq = ring.DefaultSequence()
	}
	e := &ring.Envelope{
		Version: 1,
		Core: ring.Core{
			TraceUUID: req.TraceUUID, Payload: req.Payload,
			Sequence: seq, CreatedAt: ring.Now(),
		},
		Receipts: []ring.Receipt{},
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(h.cfg.DeadlineSec)*time.Second)
	defer cancel()

	started := time.Now()
	if err := h.relay(ctx, e); err != nil {
		h.log(logEntry{Severity: "ERROR", Message: "ring failed", TraceUUID: e.Core.TraceUUID,
			HopIndex: e.HopIndex, Stage: "ring", Error: err.Error()})
		writeJSON(w, http.StatusBadGateway, e)
		return
	}

	// FR-5.3: the ring is not complete until the origin independently re-reads the
	// ring-close object from its OWN bucket and re-verifies the whole chain.
	if err := h.verifyRingClose(ctx, e); err != nil {
		e.Fail(h.cfg.Region, "final-verification", err)
		h.log(logEntry{Severity: "ERROR", Message: "final verification failed",
			TraceUUID: e.Core.TraceUUID, Stage: "final-verification", Error: err.Error()})
		writeJSON(w, http.StatusBadGateway, e)
		return
	}

	h.log(logEntry{Message: "ring complete", TraceUUID: e.Core.TraceUUID,
		HopIndex: e.HopIndex, Stage: "complete", DurationMS: time.Since(started).Milliseconds(),
		LinkHash: e.Receipts[len(e.Receipts)-1].LinkHash})
	writeJSON(w, http.StatusOK, e)
}

func (h *Handler) verifyRingClose(ctx context.Context, e *ring.Envelope) error {
	if err := e.Verify(); err != nil {
		return err
	}
	if len(e.Receipts) != len(e.Core.Sequence)+1 {
		return fmt.Errorf("expected %d receipts, got %d", len(e.Core.Sequence)+1, len(e.Receipts))
	}
	last := e.Receipts[len(e.Receipts)-1]
	if !last.RingClose || last.Region != h.cfg.Region {
		return fmt.Errorf("final receipt is not a ring close in %s", h.cfg.Region)
	}
	_, obj, err := h.st.Read(ctx, last.Object)
	if err != nil {
		return fmt.Errorf("ring-close object %s not readable: %w", last.Object, err)
	}
	if obj.CRC32C != last.CRC32C {
		return fmt.Errorf("ring-close object crc %s does not match receipt %s", obj.CRC32C, last.CRC32C)
	}
	return nil
}

func (h *Handler) cors(w http.ResponseWriter) {
	if h.cfg.AllowedOrigin == "" {
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", h.cfg.AllowedOrigin) // exact origin, never *
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	w.Header().Set("Vary", "Origin")
}

func decode(w http.ResponseWriter, r *http.Request) (*ring.Envelope, bool) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return nil, false
	}
	var e ring.Envelope
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&e); err != nil {
		http.Error(w, "bad envelope", http.StatusBadRequest)
		return nil, false
	}
	return &e, true
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}
