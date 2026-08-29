#!/usr/bin/env bash
# Two-pass apply. Cloud Run assigns opaque URLs that cannot be known before creation,
# and the ring is a cycle, so the peer map can only be wired after everything exists.
#
# Pass 1: create all services with placeholder peers.
# Pass 2: feed the real URLs back in.
#
# peers.auto.tfvars holds the public URLs and is gitignored.
set -euo pipefail
cd "$(dirname "$0")/../terraform"
export PATH="/opt/homebrew/bin:$PATH"

echo "== pass 1: create =="
terraform apply -auto-approve "$@"

echo
echo "== generating peers.auto.tfvars from real URLs =="
terraform output -json | python3 ../scripts/gen-peers.py
echo
echo "== pass 2: wire the ring =="
terraform apply -auto-approve "$@"

WIRED=$(terraform output -raw peer_map_wired)
echo
echo "peer map wired: $WIRED"
[[ "$WIRED" == "true" ]] || { echo "!! peer map still not wired"; exit 1; }
echo "run ./scripts/verify-deploy.sh next"
