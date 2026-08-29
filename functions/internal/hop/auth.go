package hop

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"

	"google.golang.org/api/idtoken"
)

// Service-to-service auth. Cloud vs local is a RUNTIME decision, not a build tag:
// Cloud Build's buildpacks compile without custom tags, so a tag-gated cloud path
// would silently vanish in the deployed binary.

// authorize attaches an OIDC ID token whose audience is the callee's base URL.
func (h *Handler) authorize(ctx context.Context, req *http.Request, audience string) error {
	if h.cfg.Local {
		return nil
	}
	ts, err := idtoken.NewTokenSource(ctx, audience)
	if err != nil {
		return err
	}
	tok, err := ts.Token()
	if err != nil {
		return err
	}
	tok.SetAuthHeader(req)
	return nil
}

// verifyCaller enforces auth on /close in-handler. /ring and /close share one Cloud Run
// service, and `allow_unauthenticated` is service-level, so the service must be public
// and /close must check for itself (Design §6). Only the final hop of the ring may call it.
func (h *Handler) verifyCaller(r *http.Request) error {
	if h.cfg.Local {
		return nil
	}
	authz := r.Header.Get("Authorization")
	raw, ok := strings.CutPrefix(authz, "Bearer ")
	if !ok {
		return fmt.Errorf("missing bearer token")
	}
	// Audience is this service's own base URL, as seen by the caller.
	payload, err := idtoken.Validate(r.Context(), raw, h.cfg.SelfURL)
	if err != nil {
		return fmt.Errorf("invalid id token: %w", err)
	}
	want := os.Getenv("RATW_CLOSER_SA")
	if want == "" {
		return fmt.Errorf("RATW_CLOSER_SA is not configured")
	}
	if email, _ := payload.Claims["email"].(string); email != want {
		return fmt.Errorf("caller is not the authorized ring-closing service account")
	}
	return nil
}
