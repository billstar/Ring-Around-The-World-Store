#!/usr/bin/env python3
"""Render a ring response as a table, and independently re-verify the hash chain.

This is a deliberate second implementation of the canonical form and chain rules --
if it agrees with the Go implementation, the format is genuinely portable, which is
what the browser verifier will depend on."""
import json, sys, hashlib

CITY = {"us-west1": "Oregon", "us-central1": "Iowa", "us-east4": "N. Virginia",
        "europe-west1": "Belgium", "europe-central2": "Warsaw", "asia-northeast1": "Tokyo"}

def canonical(o):
    # Mirrors functions/internal/canonical: sorted keys, no HTML escaping,
    # tightest separators, no trailing newline.
    return json.dumps(o, sort_keys=True, separators=(",", ":"), ensure_ascii=False).encode()

def verify(env):
    prev = hashlib.sha256(canonical(env["core"])).hexdigest()
    for i, r in enumerate(env.get("receipts") or []):
        if r.get("prev_link_hash") != prev:
            return False, f"receipt {i} ({r['region']}): prev_link_hash breaks the chain"
        body = {k: v for k, v in r.items() if k != "link_hash"}
        want = hashlib.sha256(bytes.fromhex(prev) + canonical(body)).hexdigest()
        if want != r.get("link_hash"):
            return False, f"receipt {i} ({r['region']}): link_hash mismatch"
        prev = r["link_hash"]
    return True, prev

env = json.load(open(sys.argv[1]))
receipts = env.get("receipts") or []

print(f"{'#':<3}{'region':<17}{'location':<14}{'gen':>17}  {'write':>7}{'read':>7}{'total':>8}  ok")
print("-" * 82)
for r in receipts:
    tag = " (ring close)" if r.get("ring_close") else ""
    print(f"{r['hop_index']:<3}{r['region']:<17}{(CITY.get(r['region'],'')+tag):<14}"
          f"{r['generation']:>17}  {r['d_write_us']/1000:>6.1f}m{r['d_read_us']/1000:>6.1f}m"
          f"{r['d_total_us']/1000:>7.1f}m  {'yes' if r['verified_readback'] else 'NO'}")

ok, detail = verify(env)
print()
print(f"receipts: {len(receipts)}   regions visited: {len(set(r['region'] for r in receipts))}")
print(f"chain (independent python verifier): {'VERIFIED' if ok else 'BROKEN'}")
print(f"head hash: {detail}" if ok else f"  -> {detail}")
if env.get("failure"):
    f = env["failure"]
    print(f"\nFAILURE at hop {f['hop_index']} ({f['region']}) during {f['stage']}:\n  {f['error']}")
sys.exit(0 if ok else 1)
