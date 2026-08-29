# Ring Around The World Store

A small string is carried through six GCP regions by nested **blocking** Cloud Run
calls, written to and read back from a regional GCS bucket in each, and returned to
its origin — producing a tamper-evident, independently-anchored proof that the data
physically visited every region.

```
client → us-west1 → us-central1 → us-east4 → europe-west1 → europe-central2 → asia-northeast1
              ↑                                                                      │
              └────────────────────── ring close (7th receipt) ───────────────────────┘
```

Every arrow is a blocking call. Each hop waits for the entire remainder of the chain
before returning, so the stack unwinds Tokyo → Warsaw → Belgium → Virginia → Iowa →
Oregon, and the client's request is answered only after the ring has fully closed.

See [Requirements.md](Requirements.md) and [Design.md](Design.md).

## What it proves

Each hop appends a receipt containing the GCS generation number, CRC32C, and timing
for its own write, chained as `link_n = SHA-256(link_n-1 || canonical(receipt_n))`.
Every hop re-verifies the whole inbound chain before adding to it, and the browser
re-verifies independently with WebCrypto.

The hash chain alone proves internal consistency. The real strength is *anchoring*:
each receipt names an object in a regional bucket, written by a service account that
can write only that one bucket. Forging a ring means rewriting seven objects across
six regions under six distinct identities.

Three independent implementations of the chain — Go, Python, and browser JS — are
cross-checked against a pinned golden vector by `scripts/verify-browser-impl.mjs`.

## Run it locally (no GCP, no cost)

```bash
./scripts/local-ring.sh                        # six hops as local processes
RATW_BREAK=europe-central2 ./scripts/local-ring.sh   # partial-chain 502 path
cd functions && go test ./...                  # 12 tamper vectors + canonical form
```

## Deploy

```bash
gcloud auth application-default login
terraform -chdir=terraform apply
./scripts/verify-deploy.sh
```

Then open the client:

```bash
open "$(terraform -chdir=terraform output -raw web_url)"
```

Tear down completely:

```bash
terraform -chdir=terraform destroy
```

## A note on URLs

`/ring` is public and unauthenticated by design — it must be reachable by a browser
and by external graders. Service URLs are therefore deliberately **not** committed:
they live in Terraform state (gitignored) and in deploy-time environment variables.
The browser receives the origin URL from `/config.js`, rendered at runtime.

This keeps URLs out of repository crawls; it is not an access control. The actual
cost control is `max_instances`, which bounds the amplification of a public endpoint
that fans out to seven writes per call.

## Layout

| Path | What |
|---|---|
| `functions/internal/canonical` | the deterministic JSON encoder everything hashes through |
| `functions/internal/ring` | envelope schema, validation, hash chain, tamper tests |
| `functions/internal/hop` | `/ring` `/hop` `/close`, blocking forward, deadline propagation |
| `functions/internal/store` | GCS and filesystem stores behind one interface |
| `web/` | TypeScript client: SPA, `/logs` proxy, demo mode |
| `terraform/` | 6 buckets, 7 service accounts, 7 functions, per-hop IAM |
| `scripts/` | local ring, deploy verification, cross-implementation check |
