# Ring Around The World Store — Requirements

## 1. Purpose

Demonstrate a synchronous, globe-circling chain of custody: a small client-supplied
string is carried through six GCP regions, written to and read back from a regional
GCS bucket in each, and returned to its origin — producing a tamper-evident,
independently-anchored proof that the data physically visited every region.

This is a proof-of-concept. It is not load-bearing, is not multi-tenant, and is
explicitly optimized for demonstrability over throughput.

## 2. The Ring

| Hop | Region            | Location            | Role                          |
|-----|-------------------|---------------------|-------------------------------|
| 0   | `us-west1`        | Oregon              | Origin — public ingress       |
| 1   | `us-central1`     | Iowa                | Relay                         |
| 2   | `us-east4`        | N. Virginia         | Relay                         |
| 3   | `europe-west1`    | Belgium             | Relay                         |
| 4   | `europe-central2` | Warsaw              | Relay                         |
| 5   | `asia-northeast1` | Tokyo               | Relay — last outbound hop     |
| 6   | `us-west1`        | Oregon              | Ring close (origin, 2nd write)|

Seven write/read/receipt operations across six distinct regions. Five nested
blocking calls between the client and Tokyo, satisfying the ≥5-hop requirement.

## 3. Functional Requirements

### FR-1 — Client request
- **FR-1.1** The client generates a trace UUID (v4) and submits it with a payload
  string and an ordered list of GCP region names to the origin service.
- **FR-1.2** The payload string is limited to 4 KiB.
- **FR-1.3** The client MAY supply the region sequence. It MUST be expressed as GCP
  region names only. URLs, hostnames, IPs, or any other addressing form are rejected.
- **FR-1.4** If no sequence is supplied, the canonical six-region ring (§2) is used.

### FR-2 — Sequence validation (enforced at *every* hop, not just ingress)
- **FR-2.1** Every region name MUST appear in a compile-time allowlist of the six
  deployed regions. Unknown names → `400`.
- **FR-2.2** Region names are resolved to service URLs via a server-side map injected
  by environment variable. A hop never dials an address derived from request content.
- **FR-2.3** The sequence MUST contain no duplicates and MUST be 2–8 entries long.
- **FR-2.4** `hop_index` MUST strictly increase along the chain. A hop receiving an
  index ≤ its own recorded position for that trace rejects the request. This bounds
  the ring and prevents amplification loops.
- **FR-2.5** The final outbound hop closes the ring by calling the origin region with
  `ring_close: true`, which is the sole permitted revisit of an already-visited region.

### FR-3 — Per-hop operation (identical at every hop)
Each hop, in order:
1. Validate the envelope (§FR-2) and verify the inbound hash chain (§FR-4).
2. **Write** the envelope to that region's regional GCS bucket.
3. **Read back** the object and verify byte-for-byte equality and CRC32C.
4. Append a signed receipt (§FR-4) to the envelope.
5. Emit a structured Cloud Logging event carrying the trace UUID.
6. Forward the envelope to the next hop and **block** on the reply.
7. On reply, append nothing; return the reply upward unchanged apart from its own
   unwind timestamp.

### FR-4 — Chain of custody
- **FR-4.1** Each receipt records: region, hop index, bucket, object name, GCS
  generation number, CRC32C, payload SHA-256, wall-clock timestamps, and monotonic
  intra-hop durations.
- **FR-4.2** Each receipt carries `link_hash = SHA-256(prev_link_hash || canonical_json(receipt_body))`.
  The genesis link hashes the immutable request core (trace UUID, payload, sequence).
- **FR-4.3** Every hop MUST verify the full inbound chain before doing its own work.
  A broken link is a hard failure (§FR-6), not a warning.
- **FR-4.4** Canonical JSON serialization (sorted keys, no insignificant whitespace)
  is a shared, single-implementation concern so hashes are reproducible.
- **FR-4.5** Independent anchoring: because each hop's receipt is also persisted in
  that region's own bucket, forging the chain requires coordinated rewrites of seven
  objects across six regional buckets under six distinct service accounts.

### FR-5 — Ring close and final verification
- **FR-5.1** Tokyo forwards to the origin's `/close` route and blocks on it.
- **FR-5.2** The origin's `/close` performs a full write/read/receipt cycle producing
  the seventh and final receipt, then returns.
- **FR-5.3** Only after `/close` returns does the origin's ingress handler
  re-read the ring-close object from its own bucket, verify the complete
  seven-link chain, and return `200` to the client.

### FR-6 — Failure semantics
- **FR-6.1** Any failure — validation, GCS, chain verification, downstream error,
  timeout — back-propagates as `502` with the envelope in its current partial state.
- **FR-6.2** The envelope gains a `failure` object naming the region, hop index,
  stage, and error. Intermediate hops MUST pass the original failure through
  unmodified rather than replacing it with their own.
- **FR-6.3** No retries anywhere. A retry mid-ring duplicates downstream writes and
  corrupts the chain's meaning.
- **FR-6.4** The client can therefore always see exactly how far the ring got.

### FR-7 — Observability
- **FR-7.1** Every hop emits structured JSON logs to Cloud Logging including
  `trace_uuid`, `hop_index`, `region`, `stage`, `duration_ms`, and `link_hash`.
- **FR-7.2** Logs are correlated across regions via the `X-Cloud-Trace-Context`
  header so the full ring appears as one Cloud Trace waterfall.
- **FR-7.3** A single Log Explorer query on `trace_uuid` returns all events for one ring.

### FR-8 — Web client deployment
- **FR-8.1** The web client is deployed as its own Cloud Run function (`ratw-web`) in
  `us-west1`, publicly accessible and unauthenticated. It is **not** a hop in the ring
  and writes no receipts.
- **FR-8.2** It serves two things from one origin: the static single-page UI, and a
  read-only `/logs` API (§FR-10).
- **FR-8.3** Written in TypeScript on the Node runtime, so it can use the official
  `@google-cloud/logging` client. The six ring hops remain Go; this is the only
  TypeScript in the project.
- **FR-8.4** Because the page is served from `ratw-web` and calls `/ring` on
  `ratw-origin` (a different hostname), the origin MUST return CORS headers permitting
  the web client's exact origin. Wildcard `*` is not used.

### FR-9 — Client UI
- **FR-9.1** Payload input, "Send Around The World" button, live per-hop result table
  (region, city, write/read status, per-hop and cumulative latency), and a raw envelope view.
- **FR-9.2** The page independently re-verifies the returned hash chain in the browser
  using WebCrypto — the proof is checkable client-side, not merely asserted by the server.
- **FR-9.3** The client persists every trace UUID it initiates to `localStorage`, with
  its submission timestamp and terminal status, retaining the most recent 100 and
  evicting oldest-first. This list survives page reload and is the **only** record of
  which rings belong to this client.
- **FR-9.4** A "history" panel renders those up-to-100 rings newest-first, each
  expandable to its per-hop log detail (§FR-10).

### FR-10 — Log-backed history
- **FR-10.1** The client polls `POST /logs` on a fixed interval (default 5 s, pausable)
  with its remembered trace UUIDs, and renders per-hop observability data for the most
  recent 100 completed chains.
- **FR-10.2** `/logs` runs the Cloud Logging TypeScript client server-side under the
  `ratw-web` service account, which holds `roles/logging.viewer` and nothing else.
  **Credentials never reach the browser.**
- **FR-10.3** `/logs` accepts only a list of UUID-shaped strings (max 100, regex-validated)
  and interpolates them into a fixed filter template. No client-supplied text reaches
  the filter string unvalidated — a Logging-filter injection is treated as seriously as
  a SQL injection would be.
- **FR-10.4** The response is grouped by trace UUID, then by hop index, exposing each
  hop's region, stage events, durations, and link hash.
- **FR-10.5** Cloud Logging ingestion lag (typically 1–5 s) means a just-completed ring
  may briefly show as incomplete. The UI labels this state "awaiting logs" rather than
  rendering it as a failure.
- **FR-10.6** Polling stops when the tab is hidden (`visibilitychange`) and when no
  remembered UUID is younger than 10 minutes — this is a demo, not a monitoring system,
  and it should idle at zero cost.

### FR-11 — Demo mode
- **FR-11.1** A toggle in the UI starts demo mode.
- **FR-11.2** While active, the client generates its own unique message per iteration
  (a monotonic counter, timestamp, and short random token, so every payload — and thus
  every payload hash — is distinct) and sends rings **sequentially**, each starting only
  after the previous one has returned.
- **FR-11.3** Demo mode stops after a maximum of 100 iterations, on user toggle-off, or
  on page unload. It is never unbounded.
- **FR-11.4** Sequential-only is a hard requirement, not a simplification: concurrent
  rings would interleave in the history view and make the latency numbers unreadable.
  At ~2 s per ring, a full 100-iteration run takes roughly 3–5 minutes.
- **FR-11.5** A failed ring does not abort the demo. It is recorded with its partial
  chain and the loop continues to the next iteration.
- **FR-11.6** A live progress indicator shows iteration N of 100, success/failure
  counts, and rolling mean end-to-end latency.
- **FR-11.7** A 100-iteration run produces ~700 GCS objects and a few thousand log
  entries. Both fall inside free-tier limits, and the 7-day object lifecycle (§NFR-7,
  Design §7) reclaims the storage.

## 4. Non-Functional Requirements

- **NFR-1 Auth.** Hops 1–5 and `/close` require service-account OIDC ID tokens and are
  deployed `--no-allow-unauthenticated`. Only the origin's ingress route is public.
- **NFR-2 Least privilege.** Each region's function runs as its own service account with
  object read/write on only its own bucket, plus invoker rights on only the next hop
  (and, for Tokyo, on origin `/close`).
- **NFR-3 Latency budget.** Expected client-observed total 1.5–4 s. Per-hop deadlines
  decrease down the chain so an inner timeout always fires before its caller's.
- **NFR-4 Timing fidelity.** Wall-clock timestamps carry single-digit-ms cross-region
  skew (Google NTP, leap-smeared). Intra-hop durations use a monotonic clock and are
  exact. The receipt schema documents this distinction; no TrueTime-style uncertainty
  bound is claimed.
- **NFR-5 IaC.** All infrastructure in Terraform. `terraform destroy` fully tears down
  six regions in one command — this matters for cost.
- **NFR-6 Language/runtime.** Ring hops: Go, deployed as Cloud Run functions (gen2) —
  one codebase, one binary, deployed six times, differentiated only by environment
  variables. Web tier: TypeScript on Node, a seventh separate Cloud Run function.
- **NFR-7 Cost.** Idle cost ≈ storage only (bytes). Scale-to-zero everywhere;
  no min-instances. Log polling idles to zero when the tab is hidden (FR-10.6), and
  objects expire after 7 days.

## 5. Explicit Non-Goals

- Concurrency, throughput, or load testing.
- Cryptographic signing with KMS (hash chain + multi-region anchoring is the proof).
  Noted as a stretch goal in Design.md §9.
- Private networking via VPC/PSC. Traffic rides Google's backbone between `run.app`
  endpoints, authenticated but over public edges.
- Persistence lifecycle, GC, or retention policy beyond a 7-day object TTL.
- Any authentication of the *end user*. Both the ingress and the web client are open.
- Cross-client visibility. A browser sees only rings whose UUIDs it holds in its own
  `localStorage`; clearing storage orphans them. There is no server-side index of
  trace UUIDs, by design — Cloud Logging *is* the index.

## 6. Acceptance Criteria

1. A client request returns `200` with seven receipts spanning six regions.
2. The browser independently verifies the seven-link hash chain.
3. Seven objects exist across six regional buckets, verifiable by `gsutil`.
4. One Cloud Logging query on the trace UUID returns all hops with per-region latency.
5. Deliberately breaking one hop (e.g. revoking its bucket IAM) yields a `502`
   carrying a partial chain that correctly identifies the failing region.
6. `terraform apply` from empty project to working ring; `terraform destroy` leaves nothing.
7. The web client, loaded from its public Cloud Run URL, completes a ring and shows a
   green client-side chain verification.
8. After a page reload, the history panel repopulates from `localStorage` and re-fetches
   per-hop log detail for prior rings via `/logs`.
9. Demo mode runs 100 sequential iterations, halts on its own at exactly 100, and the
   history panel shows 100 entries with a rolling mean latency.
10. A malformed `/logs` request (non-UUID strings, >100 entries) is rejected with `400`
    and never reaches the Cloud Logging filter.
