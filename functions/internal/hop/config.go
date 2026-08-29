package hop

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"example.com/ratw/internal/ring"
)

// Config is entirely environment-driven: one binary, deployed six times, differing
// only in these values.
type Config struct {
	Region        string
	Bucket        string
	Peers         map[string]string // region -> base URL. The ONLY source of dial addresses.
	Ring          []string          // the deployed ring, used when a client sends no sequence
	IsOrigin      bool
	Local         bool   // skip OIDC; used for the local proof-of-concept
	AllowedOrigin string // exact CORS origin for /ring; never "*"
	DeadlineSec   int
	Port          string
	SelfURL       string // this service's own base URL; the audience /close validates against
}

func LoadConfig() (Config, error) {
	c := Config{
		Region:        os.Getenv("RATW_REGION"),
		Bucket:        os.Getenv("RATW_BUCKET"),
		IsOrigin:      os.Getenv("RATW_IS_ORIGIN") == "true",
		Local:         os.Getenv("RATW_LOCAL") == "true",
		AllowedOrigin: os.Getenv("RATW_ALLOWED_ORIGIN"),
		SelfURL:       strings.TrimSuffix(os.Getenv("RATW_SELF_URL"), "/"),
		Port:          os.Getenv("PORT"),
	}
	if c.Port == "" {
		c.Port = "8080"
	}
	if _, ok := ring.Regions[c.Region]; !ok {
		return c, fmt.Errorf("RATW_REGION %q is not an allowlisted region", c.Region)
	}
	if c.Bucket == "" {
		return c, fmt.Errorf("RATW_BUCKET is required")
	}
	if c.IsOrigin && !c.Local && c.SelfURL == "" {
		return c, fmt.Errorf("RATW_SELF_URL is required on the origin to validate /close callers")
	}
	c.DeadlineSec = 120
	if v := os.Getenv("RATW_DEADLINE_SEC"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return c, fmt.Errorf("RATW_DEADLINE_SEC: %w", err)
		}
		c.DeadlineSec = n
	}

	// The deployed topology is the source of truth for the default sequence. A ring
	// compiled into the binary drifts from the ring that actually exists the moment
	// deployment changes, and the hop that notices is the one that cannot forward.
	for _, r := range strings.Split(os.Getenv("RATW_RING"), ",") {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		if _, ok := ring.Regions[r]; !ok {
			return c, fmt.Errorf("RATW_RING names unknown region %q", r)
		}
		c.Ring = append(c.Ring, r)
	}

	// RATW_PEERS is the SSRF firebreak. Request content selects a KEY in this map;
	// it never supplies an address. A peer naming a non-allowlisted region is a
	// deployment bug and fails at startup rather than at request time.
	c.Peers = map[string]string{}
	for _, pair := range strings.Split(os.Getenv("RATW_PEERS"), ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		k, v, ok := strings.Cut(pair, "=")
		if !ok {
			return c, fmt.Errorf("RATW_PEERS entry %q is not region=url", pair)
		}
		if _, ok := ring.Regions[k]; !ok {
			return c, fmt.Errorf("RATW_PEERS names unknown region %q", k)
		}
		if !strings.HasPrefix(v, "https://") && !(c.Local && strings.HasPrefix(v, "http://")) {
			return c, fmt.Errorf("RATW_PEERS url for %q must be https", k)
		}
		c.Peers[k] = strings.TrimSuffix(v, "/")
	}
	return c, nil
}
