//go:build !gcp

package hop

import (
	"context"
	"fmt"
	"net/http"
)

// Local build: no OIDC. The cloud build (-tags gcp) supplies the real implementations
// in auth_gcp.go. Splitting them by build tag keeps the local proof-of-concept free of
// cloud dependencies while guaranteeing the two cannot silently diverge in signature.

func (h *Handler) authorize(_ context.Context, _ *http.Request, _ string) error {
	if !h.cfg.Local {
		return fmt.Errorf("built without -tags gcp but RATW_LOCAL is not true")
	}
	return nil
}

func (h *Handler) verifyCaller(_ *http.Request) error {
	if !h.cfg.Local {
		return fmt.Errorf("built without -tags gcp but RATW_LOCAL is not true")
	}
	return nil
}
