locals {
  # The ring, in order. Index 0 is the origin: it takes the public request and also
  # closes the ring, so it is the only region visited twice.
  # This project's Cloud Run region-initialization quota is 5. The sixth region fails
  # to initialize regardless of WHICH region it is (asia-northeast1 and asia-east1 both
  # refused with "Project failed to initialize in this region due to quota exceeded"),
  # so the Asia hop is gated until a quota increase is granted. Flip enable_asia_hop
  # to true once it is; nothing else needs to change.
  base_ring = [
    "us-west1",        # Oregon      - origin and ring close
    "us-central1",     # Iowa
    "us-east4",        # N. Virginia
    "europe-west1",    # Belgium
    "europe-central2", # Warsaw
  ]

  ring = var.enable_asia_hop ? concat(local.base_ring, [var.asia_region]) : local.base_ring

  # Deadlines shrink down the chain so an inner timeout always fires before its
  # caller's, and a stall is attributed to the region that actually stalled.
  deadlines = merge({
    "us-west1"        = 120
    "us-central1"     = 100
    "us-east4"        = 80
    "europe-west1"    = 60
    "europe-central2" = 45
  }, var.enable_asia_hop ? { (var.asia_region) = 30 } : {})

  # Cloud Run assigns opaque, hashed URLs in this project (ratw-web-hg5yxxb66a-uw.a.run.app),
  # NOT the deterministic SERVICE-PROJECTNUMBER.REGION.run.app format. They are therefore
  # unknowable before creation, and the ring is a cycle (last hop calls the origin), so a
  # single-pass apply cannot wire the peer map.
  #
  # Two-pass apply (Design section 8): pass 1 creates everything with placeholders; the real
  # URIs are then written to peers.auto.tfvars and pass 2 wires them in. scripts/deploy.sh
  # runs both. peers.auto.tfvars is gitignored, so URLs stay out of source control.
  placeholder = "https://unwired.invalid"

  urls = length(var.peer_urls) > 0 ? var.peer_urls : {
    for r in local.ring : r => local.placeholder
  }

  web_url = var.web_url != "" ? var.web_url : local.placeholder

  wired = length(var.peer_urls) > 0

  # Every hop receives the full map. Reachability is enforced by IAM, not by what a
  # hop knows: each service account holds run.invoker on exactly one successor.
  peers = join(",", [for r in local.ring : "${r}=${local.urls[r]}"])

  origin = local.ring[0]
  # The last hop is the only caller permitted to close the ring.
  closer = local.ring[length(local.ring) - 1]
}

data "google_project" "this" {
  project_id = var.project_id
}
