# Ring Around The World Store — Design

Companion to `Requirements.md`. Targets a ~2-hour build.

**Target project:** `ring-around-the-world-store` (number `71651907801`), billing linked,
no org policies. All resources land in this project.

## 1. Topology

```
                      browser
                         │ loads page, POST /logs   (same origin)
                         ▼
              ┌───────────────────────────────┐
              │ us-west1  ratw-web  (Node/TS)  │──▶ Cloud Logging API (read-only)
              │   GET  /      static SPA       │
              │   POST /logs  log query proxy  │
              └───────────────────────────────┘
                         ╎ (not a hop — writes nothing)
   browser ──────────────╎── POST /ring  (public, unauthenticated, CORS)
   │                                                                    
   ▼
┌──────────────────────────────────────────────────────────────────────┐
│ us-west1  ratw-origin                                                │
│   /ring   ── writes receipt 0 ──┐                    ▲               │
│   /close  ── writes receipt 6 ──┼── (blocked) ───────┘               │
└─────────────────────────────────┼────────────────────────────────────┘
                                  ▼ OIDC
      us-central1 ──▶ us-east4 ──▶ europe-west1 ──▶ europe-central2 ──▶ asia-northeast1
        rcpt 1        rcpt 2         rcpt 3            rcpt 4              rcpt 5
                                                                             │
                                  ┌──────────────────────────────────────────┘
                                  ▼  POST /close  (ring_close: true)
                            us-west1 /close  → receipt 6 → returns
```

Every arrow is a blocking HTTPS call. The stack unwinds Tokyo → Warsaw → Belgium →
Virginia → Iowa → Oregon, and only then does `/ring` answer the browser.

### 1.1 The origin re-entrancy problem

`/ring` is still inside its handler when Tokyo calls `/close` on the same service.
This is safe on Cloud Run — each concurrent request gets its own instance slot — but
becomes a self-deadlock if `max_instance_count` is 1, or if concurrency is 1 with a
single instance. Mitigations, all mandatory:

- `max_instance_count = 4` on the origin (2 would suffice; 4 is headroom).
- `/ring` and `/close` are distinct routes with distinct behavior in one binary.
- `/close` never forwards. It is a terminal write-and-return. Nothing can re-enter it.

## 2. Deployment model

One Go module, one entrypoint, seven deployments (six functions; origin serves two
routes). Behavior is entirely env-driven:

| Env var          | Example                                   | Meaning                        |
|------------------|-------------------------------------------|--------------------------------|
| `RATW_REGION`    | `europe-central2`                         | This hop's identity            |
| `RATW_BUCKET`    | `ratw-europe-central2-<suffix>`           | This hop's regional bucket     |
| `RATW_PEERS`     | `us-west1=https://…,us-central1=https://…`| region → URL map (the ONLY source of dial addresses) |
| `RATW_IS_ORIGIN` | `true` / `false`                          | Enables `/ring` and `/close`   |

`RATW_PEERS` is the SSRF firebreak: a hop can only ever dial a URL Terraform put in
its own environment. Request content selects a *key*, never an address.

## 3. Envelope schema

```jsonc
{
  "version": 1,
  "core": {                                  // immutable; genesis hash covers exactly this
    "trace_uuid": "8f14e45f-...",
    "payload": "hello world",
    "sequence": ["us-west1","us-central1","us-east4",
                 "europe-west1","europe-central2","asia-northeast1"],
    "created_at": "2026-08-29T19:04:11.221Z"
  },
  "hop_index": 3,                            // index of the hop currently handling it
  "ring_close": false,
  "receipts": [ /* Receipt, append-only */ ],
  "failure": null                            // set once, never overwritten (FR-6.2)
}
```

### 3.1 Receipt

```jsonc
{
  "hop_index": 2,
  "region": "us-east4",
  "bucket": "ratw-us-east4-a1b2c3",
  "object": "traces/8f14e45f-.../hop-02-us-east4.json",
  "generation": 1724951051221334,            // GCS object generation — the anchor
  "crc32c": "z8SuHQ==",                      // returned by GCS, compared to local
  "payload_sha256": "9b74c9897b...",
  "verified_readback": true,
  "t_received":  "2026-08-29T19:04:11.402Z", // wall clock, ±few ms cross-region (NFR-4)
  "t_written":   "2026-08-29T19:04:11.509Z",
  "t_readback":  "2026-08-29T19:04:11.548Z",
  "t_forwarded": "2026-08-29T19:04:11.551Z",
  "d_write_us":  107_000,                    // monotonic, exact
  "d_read_us":   39_000,
  "d_total_us":  149_000,
  "prev_link_hash": "4d0e...",
  "link_hash":      "a71f..."                // SHA-256(prev_link_hash || canonical(body))
}
```

`link_hash` is computed over the receipt with `link_hash` itself omitted; verification
recomputes it identically. Timestamps are *inside* the hashed body, so they are as
tamper-evident as everything else.

### 3.2 Canonical JSON

Go's `encoding/json` sorts map keys but not struct fields, and escapes HTML by default.
The chain therefore serializes through one shared helper — `canonical.Marshal` — that
marshals via `map[string]any` with `SetEscapeHTML(false)`. **This is the single most
likely source of a "chain broken" bug.** It lives in one file, is used by both the
signer and the verifier, and has the only unit test in the project that really matters.
The browser verifier (WebCrypto) reimplements the same rule in ~15 lines of JS.

## 4. Hash chain

```
genesis     = SHA256(canonical(core))
link_0      = SHA256(genesis  || canonical(receipt_0_body))
link_1      = SHA256(link_0   || canonical(receipt_1_body))
...
link_6      = SHA256(link_5   || canonical(receipt_6_body))
```

Concatenation is over the 32 raw bytes of the previous digest followed by the canonical
body bytes — not hex strings — to avoid any ambiguity. Every hop re-verifies links
`0..n-1` from the genesis forward before appending its own. The browser does the same.

**Threat model, stated plainly.** The chain alone proves internal consistency; anyone
holding the envelope could recompute a self-consistent forgery. The real strength is
the *anchoring*: each receipt names a GCS generation number in a regional bucket, and
that object contains the envelope as it stood at that moment. Forging the ring means
rewriting seven immutable-by-generation objects across six regions under six service
accounts that each can only write their own bucket. Verification is offline and
independent: fetch the seven objects, replay the chain.

## 5. Deadline propagation

Each hop computes `remaining = inbound_deadline - elapsed - reserve` and passes a
shorter deadline downstream, so an inner timeout always fires before its caller's and
the failure is attributed to the correct region.

| Hop | Deadline |
|-----|----------|
| `/ring` (client-facing) | 120 s |
| us-central1 | 100 s |
| us-east4 | 80 s |
| europe-west1 | 60 s |
| europe-central2 | 45 s |
| asia-northeast1 | 30 s |
| `/close` | 15 s |

Generous by design — the ring should complete in ~2 s, and these exist only to make a
hung hop fail cleanly and attributably.

## 6. Auth

Service-to-service calls use `google.golang.org/api/idtoken` to mint an OIDC ID token
with the callee's URL as audience. Each hop's service account holds
`roles/run.invoker` on exactly its successor — Tokyo's holds it on the origin service
(needed for `/close`), which is the one place the graph is not a simple path.

`/ring` is public; `/close` is not. Both live on the origin service, so Cloud Run's
service-level `allow_unauthenticated` cannot distinguish them: **the origin service is
deployed public, and `/close` enforces its own OIDC verification in-handler** using
`idtoken.Validate` against Tokyo's expected service account. This is the one deliberate
deviation from pure platform-level auth, and it is called out here because it is the
sort of thing a reviewer should catch and ask about.

## 7. Storage

- One regional (not multi-region) bucket per region. Regionality is the point.
- Uniform bucket-level access; public access prevention enforced.
- Object naming: `traces/<uuid>/hop-<NN>-<region>.json` — sorts correctly, unambiguous.
- Object versioning off; 7-day lifecycle delete for cleanup.
- Write with `DoesNotExist` precondition so a replayed hop collides rather than
  silently overwriting a receipt.

## 8. Terraform layout

```
functions/                     # Go module `ratw`
  cmd/ratw/main.go             # one binary, six deployments
  cmd/ratw/store_local.go      # !gcp: refuses to run without RATW_LOCAL
  internal/canonical/          # THE deterministic JSON encoder (rules 1-4)
  internal/ring/envelope.go    # schema, FR-2 validation, routing
  internal/ring/chain.go       # genesis, link hashing, full-chain verification
  internal/ring/chain_test.go  # 12 tamper vectors + canonical-form edge cases
  internal/store/store.go      # Store interface + GCS-compatible CRC32C
  internal/store/local.go      # filesystem Store (local PoC)
  internal/hop/config.go       # env-driven config; RATW_PEERS SSRF firebreak
  internal/hop/handler.go      # /ring /hop /close, admit -> process -> forward
  internal/hop/forward.go      # blocking call + deadline propagation header
  internal/hop/auth_local.go   # !gcp build tag: no OIDC
  internal/hop/log.go          # structured Cloud Logging JSON
scripts/local-ring.sh          # runs all six hops as processes; RATW_BREAK=<region>
scripts/show-ring.py           # independent 2nd chain verifier (proves portability)
terraform/
  main.tf                      # provider, APIs, random suffix
  regions.tf                   # the ring, region -> successor
  module/hop/                  # bucket + SA + IAM + function + invoker binding
  outputs.tf
web/                           # ratw-web: TypeScript on Node
  index.ts                     # GET / (static), POST /logs
  logs.ts                      # @google-cloud/logging + UUID validation
  public/index.html            # SPA: WebCrypto verifier, history, demo mode
```

**Build tags.** Cloud-only code (OIDC minting, GCS client) lives behind `-tags gcp`;
the default build has no cloud dependencies at all, so the local ring runs with zero
modules downloaded and cannot accidentally reach for credentials. `auth_local.go`
hard-fails if built without the tag while `RATW_LOCAL` is unset, so the two paths
cannot silently diverge.

Bootstrapping note: each hop needs its *successor's* URL, and the ring is a cycle (the
last hop calls the origin), so the peer map cannot be wired in a single pass.

An attempt was made to avoid this by deriving URLs from Cloud Run's deterministic
`SERVICE-PROJECTNUMBER.REGION.run.app` format, which would have made the map knowable
before creation. **This project does not use that format** — it assigns opaque hashed
URLs (`ratw-web-<hash>-uw.a.run.app`). The `peer_map_wired` output exists precisely to
catch that: it asserts every configured peer URL equals the URL Google actually
assigned, and it caught the mismatch before a single misrouted ring was sent.

The working approach is the two-pass apply, driven by `scripts/deploy.sh`:
pass 1 creates everything with `https://unwired.invalid` placeholders; the real URIs
are written to `peers.auto.tfvars` (gitignored — it holds the public URLs); pass 2
wires them in. `verify-deploy.sh` refuses to run while `peer_map_wired` is false.

## 9. Web tier (`ratw-web`)

A seventh Cloud Run function, in `us-west1`, TypeScript on Node. Public, unauthenticated,
not part of the ring, holds no bucket permissions and writes no receipts.

### 9.1 Why a server-side log proxy

`@google-cloud/logging` is a Node library and the Logging API requires credentials. There
is no browser-safe way to query it — shipping a service account key to the page would hand
every visitor read access to the project's logs. So the browser never talks to Cloud
Logging. It talks to `/logs` on the same origin that served it, and `ratw-web` performs the
query under its own service account holding `roles/logging.viewer` and nothing else.

This also solves CORS for free on the history path: page and API share an origin. Only the
`/ring` call crosses origins, and `ratw-origin` allowlists the exact `ratw-web` URL —
never `*`, since `/ring` is a state-changing public endpoint.

### 9.2 `POST /logs`

```jsonc
// request
{ "trace_uuids": ["8f14e45f-...", "..."] }   // max 100, each /^[0-9a-f-]{36}$/ validated

// response
{ "traces": { "8f14e45f-...": { "complete": true, "hops": [ /* per-hop events */ ] } },
  "lag_warning": false }
```

Validation is strict and happens before the filter is built (FR-10.3). UUIDs are
regex-checked, count-capped at 100, and interpolated into a fixed template:

```
resource.type="cloud_run_revision"
jsonPayload.trace_uuid=("8f14e45f-..." OR "...")
timestamp >= "<now-24h>"
```

100 UUIDs is ~4 KB of filter, well inside Cloud Logging's 20 KB limit. The 24-hour
timestamp bound is not cosmetic — an unbounded filter scans far more and is markedly
slower.

### 9.3 Client-side state

`localStorage` key `ratw.traces`: an array of `{uuid, payload, submitted_at, status,
total_ms}`, capped at 100 and evicted oldest-first. This is the only record tying rings to
this browser — there is no server-side index. Cloud Logging is the index, and the UUID is
the key.

Polling: 5 s interval, paused on `visibilitychange`, and stopped entirely once no
remembered UUID is younger than 10 minutes. A just-returned ring whose logs have not landed
yet renders as "awaiting logs" (ingestion lag is normally 1–5 s), not as a failure — the
single most likely thing to be misread as a bug during a live demo.

### 9.4 Demo mode

A plain sequential loop, deliberately not concurrent:

```
for i in 1..100:
    if !enabled: break
    payload = `demo-${i}-${Date.now()}-${rand}`   // unique ⇒ unique payload hash
    result  = await sendRing(payload)             // awaited: one ring in flight at a time
    record(result)                                // failures recorded, loop continues
    await sleep(250ms)
```

Bounded at 100 iterations, and stopped by toggle-off, page unload, or reaching the cap.
Concurrency would interleave rings in the history view and make the latency numbers
meaningless, which is the entire point of the panel. Expect 3–5 minutes for a full run,
~700 GCS objects, and a few thousand log entries — all inside free tier, all reclaimed by
the 7-day lifecycle rule.

Failures do not abort the run (FR-11.5): a demo that survives a broken hop and shows the
partial chain is a better demo than one that stops.

## 9.5 The deployed ring is authoritative

`RATW_RING` carries the deployed topology to every hop, and the origin uses it as the
default sequence when a client supplies none. An earlier version compiled the canonical
six-region ring into the binary, which drifted the moment deployment changed: with five
regions deployed, Warsaw dutifully tried to forward to a Tokyo that did not exist and
failed with `no peer configured for region "asia-northeast1"`. The topology is
configuration, not a constant.

## 10. Stretch goals (only if time remains)

1. **Per-region KMS signatures.** Each hop signs its `link_hash` with a regional
   Cloud KMS asymmetric key. Upgrades the proof from "tamper-evident" to
   "cryptographically attributable per region". ~30 min, and the natural next step.
2. Log-based metric + dashboard of per-hop latency.
3. A `verify` CLI that fetches all seven objects and replays the chain offline.

## 11. Build order

1. `chain.go` + `chain_test.go` — the hash chain, proven locally before any cloud.
2. `main.go` / `envelope.go` / `store.go` — full hop logic, testable against one bucket.
3. Terraform for one region; deploy; verify a one-hop "ring".
4. Expand to six regions; two-pass apply.
5. `web/public/index.html` — send a ring, independent WebCrypto verification, per-hop table.
6. Break a hop deliberately; confirm the partial-chain 502.
7. `ratw-web` `/logs` proxy + `localStorage` history panel.
8. Demo mode loop.

Steps 1–6 are the deliverable. Steps 7–8 are additive and touch nothing in 1–6, so if the
clock runs out the project still stands on its own.
