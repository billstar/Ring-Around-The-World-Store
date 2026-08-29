// Package function is the Cloud Run functions (gen2) entrypoint. It is a thin
// adapter: all behavior lives in internal/hop, so the deployed service and the local
// binary in cmd/ratw run byte-identical logic.
package function

import (
	"context"
	"log"
	"net/http"

	"github.com/GoogleCloudPlatform/functions-framework-go/functions"

	"ratw/internal/hop"
	"ratw/internal/store"
)

func init() {
	cfg, err := hop.LoadConfig()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	st, err := store.NewGCS(context.Background(), cfg.Bucket)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	h := hop.New(cfg, st)
	functions.HTTP("Ring", func(w http.ResponseWriter, r *http.Request) { h.ServeHTTP(w, r) })
}
