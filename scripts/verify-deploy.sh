#!/usr/bin/env bash
# Post-deploy verification against the real ring. Reads URLs from Terraform state at
# runtime; never prints the full origin URL, so output is safe to paste anywhere.
set -euo pipefail
cd "$(dirname "$0")/.."
export PATH="/opt/homebrew/bin:$PATH"
PROJECT="${RATW_PROJECT:-ring-around-the-world-store}"

ORIGIN=$(terraform -chdir=terraform output -raw origin_url)
MATCH=$(terraform -chdir=terraform output -raw peer_map_wired)


echo "== 1. deterministic URL assumption =="
echo "   peer map wired to real URLs: $MATCH"
echo "   "
[[ "$MATCH" == "true" ]] || { echo "   !! peer map points at URLs Google did not assign"; exit 1; }

echo
echo "== 2. send one ring around the world =="
UUID=$(uuidgen | tr 'A-Z' 'a-z')
START=$(python3 -c 'import time;print(int(time.time()*1000))')
HTTP=$(curl -s -o /tmp/ratw-deploy.json -w '%{http_code}' \
  -X POST "$ORIGIN/ring" -H 'Content-Type: application/json' \
  -d "{\"trace_uuid\":\"$UUID\",\"payload\":\"verification ring\"}")
END=$(python3 -c 'import time;print(int(time.time()*1000))')
echo "   trace $UUID -> HTTP $HTTP in $((END-START))ms"
python3 scripts/show-ring.py /tmp/ratw-deploy.json

echo
echo "== 3. objects actually present in each regional bucket =="
for r in $(terraform -chdir=terraform output -json buckets | python3 -c "import json,sys;print(' '.join(json.load(sys.stdin)))"); do
  b=$(terraform -chdir=terraform output -json buckets | python3 -c "import json,sys;print(json.load(sys.stdin)['$r'])")
  n=$(gcloud storage ls "gs://$b/traces/$UUID/**" --project "$PROJECT" 2>/dev/null | wc -l | tr -d ' ')
  loc=$(gcloud storage buckets describe "gs://$b" --project "$PROJECT" --format='value(location)' 2>/dev/null)
  printf "   %-17s %s object(s)  bucket location=%s\n" "$r" "$n" "$loc"
done

echo
echo "== 4. one log query returns the whole ring =="
sleep 8  # Cloud Logging ingestion lag
gcloud logging read "jsonPayload.trace_uuid=\"$UUID\"" --project "$PROJECT" --limit 60 \
  --format='value(jsonPayload.hop_index,jsonPayload.region,jsonPayload.stage,jsonPayload.duration_ms)' \
  2>/dev/null | sort -n | sed 's/^/   /'

echo
echo "== 5. hops reject unauthenticated callers =="
for r in $(terraform -chdir=terraform output -json buckets | python3 -c "import json,sys;print(' '.join(list(json.load(sys.stdin))[1:3]))"); do
  u=$(terraform -chdir=terraform output -json actual_urls | python3 -c "import json,sys;print(json.load(sys.stdin)['$r'])")
  [[ -z "$u" ]] && continue
  code=$(curl -s -o /dev/null -w '%{http_code}' -X POST "$u/hop" -d '{}' --max-time 15)
  printf "   %-17s unauthenticated POST /hop -> HTTP %s %s\n" "$r" "$code" \
    "$([[ "$code" == "403" || "$code" == "401" ]] && echo '(correctly refused)' || echo '(!! expected 401/403)')"
done
echo
echo "Done. Open the client with: terraform -chdir=terraform output -raw web_url"
