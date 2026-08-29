#!/usr/bin/env bash
# Runs the entire six-region ring locally as six processes. No GCP, no cost.
# Each process is the same binary with a different RATW_REGION.
set -euo pipefail
cd "$(dirname "$0")/.."
export PATH="/opt/homebrew/bin:$PATH"

WORK="${RATW_WORK:-/tmp/ratw-local}"
rm -rf "$WORK"; mkdir -p "$WORK/logs"

REGIONS=(us-west1 us-central1 us-east4 europe-west1 europe-central2 asia-northeast1)
PORTS=(8081 8082 8083 8084 8085 8086)

# Every hop gets the full peer map. A hop can only ever dial a URL that appears here —
# request content selects a key, never an address.
PEERS=""
for i in "${!REGIONS[@]}"; do
  PEERS+="${REGIONS[$i]}=http://127.0.0.1:${PORTS[$i]},"
done
PEERS="${PEERS%,}"

echo "building..."
(cd functions && go build -o "$WORK/ratw" ./cmd/ratw)

PIDS=()
cleanup() { for p in "${PIDS[@]:-}"; do kill "$p" 2>/dev/null || true; done; }
trap cleanup EXIT

# RATW_BREAK=<region> leaves that hop down, to exercise the partial-chain 502 path.
for i in "${!REGIONS[@]}"; do
  region="${REGIONS[$i]}"
  if [[ "${RATW_BREAK:-}" == "$region" ]]; then
    echo "!! deliberately NOT starting $region"
    continue
  fi
  origin=false; [[ "$region" == "us-west1" ]] && origin=true
  RATW_REGION="$region" \
  RATW_BUCKET="ratw-$region-local" \
  RATW_LOCAL_DIR="$WORK/buckets/$region" \
  RATW_PEERS="$PEERS" \
  RATW_IS_ORIGIN="$origin" \
  RATW_LOCAL=true \
  RATW_ALLOWED_ORIGIN="http://127.0.0.1:8080" \
  PORT="${PORTS[$i]}" \
  "$WORK/ratw" > "$WORK/logs/$region.log" 2>&1 &
  PIDS+=($!)
done

echo -n "waiting for hops"
for i in "${!PORTS[@]}"; do
  [[ "${RATW_BREAK:-}" == "${REGIONS[$i]}" ]] && continue
  for _ in $(seq 1 50); do
    curl -sf "http://127.0.0.1:${PORTS[$i]}/healthz" >/dev/null 2>&1 && break
    sleep 0.1
  done
  echo -n "."
done
echo " up"

UUID=$(uuidgen | tr 'A-Z' 'a-z')
PAYLOAD="${1:-hello from california}"
echo "trace: $UUID"
echo

START=$(python3 -c 'import time;print(int(time.time()*1000))')
HTTP=$(curl -s -o "$WORK/response.json" -w '%{http_code}' \
  -X POST "http://127.0.0.1:8081/ring" \
  -H 'Content-Type: application/json' \
  -d "{\"trace_uuid\":\"$UUID\",\"payload\":\"$PAYLOAD\"}")
END=$(python3 -c 'import time;print(int(time.time()*1000))')

echo "HTTP $HTTP in $((END-START))ms"
echo
python3 scripts/show-ring.py "$WORK/response.json"

echo
echo "objects written across regional buckets:"
find "$WORK/buckets" -name '*.json' | sed "s|$WORK/buckets/||" | sort | sed 's/^/  /'

echo
echo "logs: $WORK/logs/  response: $WORK/response.json"
[[ -n "${RATW_BREAK:-}" ]] && exit 0
[[ "$HTTP" == "200" ]] || exit 1
