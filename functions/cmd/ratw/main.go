// Command ratw runs one hop of the ring. One binary, deployed six times; behavior is
// entirely determined by environment variables (Design §2).
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"ratw/internal/hop"
	"ratw/internal/store"
)

func main() {
	cfg, err := hop.LoadConfig()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	var st store.Store
	if cfg.Local {
		dir := os.Getenv("RATW_LOCAL_DIR")
		if dir == "" {
			dir = filepath.Join(os.TempDir(), "ratw", cfg.Bucket)
		}
		st, err = store.NewLocal(dir, cfg.Bucket)
	} else {
		st, err = store.NewGCS(context.Background(), cfg.Bucket)
	}
	if err != nil {
		log.Fatalf("store: %v", err)
	}

	h := hop.New(cfg, st)
	log.Printf("ratw %s listening on :%s (origin=%v local=%v bucket=%s peers=%d)",
		cfg.Region, cfg.Port, cfg.IsOrigin, cfg.Local, cfg.Bucket, len(cfg.Peers))
	if err := http.ListenAndServe(":"+cfg.Port, h); err != nil {
		log.Fatal(err)
	}
}
