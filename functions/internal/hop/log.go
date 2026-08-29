package hop

import (
	"encoding/json"
	"fmt"
	"os"
)

// Structured logs on stdout become Cloud Logging jsonPayload entries verbatim.
// The web tier queries these by trace_uuid (FR-10), so these field names are API.
type logEntry struct {
	Severity   string `json:"severity"`
	Message    string `json:"message"`
	TraceUUID  string `json:"trace_uuid"`
	ClientID   string `json:"client_id,omitempty"` // set by the origin only; scopes /logs
	HopIndex   int    `json:"hop_index"`
	Region     string `json:"region"`
	Stage      string `json:"stage"`
	DurationMS int64  `json:"duration_ms,omitempty"`
	LinkHash   string `json:"link_hash,omitempty"`
	Object     string `json:"object,omitempty"`
	Generation int64  `json:"generation,omitempty"`
	Error      string `json:"error,omitempty"`
	Trace      string `json:"logging.googleapis.com/trace,omitempty"`
}

func (h *Handler) log(e logEntry) {
	if e.Severity == "" {
		e.Severity = "INFO"
	}
	e.Region = h.cfg.Region
	if e.Trace == "" && h.project != "" && e.TraceUUID != "" {
		// Correlates the whole ring into one Cloud Trace waterfall (FR-7.2).
		e.Trace = fmt.Sprintf("projects/%s/traces/%s", h.project, e.TraceUUID)
	}
	b, err := json.Marshal(e)
	if err != nil {
		return
	}
	os.Stdout.Write(append(b, '\n'))
}
