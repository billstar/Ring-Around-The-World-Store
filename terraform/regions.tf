locals {
  # The ring, in order. Index 0 is the origin: it takes the public request and also
  # closes the ring, so it is the only region visited twice.
  ring = [
    "us-west1",        # Oregon
    "us-central1",     # Iowa
    "us-east4",        # N. Virginia
    "europe-west1",    # Belgium
    "europe-central2", # Warsaw
    "asia-northeast1", # Tokyo
  ]

  # Deadlines shrink down the chain so an inner timeout always fires before its
  # caller's, and a stall is attributed to the region that actually stalled.
  deadlines = {
    "us-west1"        = 120
    "us-central1"     = 100
    "us-east4"        = 80
    "europe-west1"    = 60
    "europe-central2" = 45
    "asia-northeast1" = 30
  }

  # Cloud Run's deterministic URL format lets every hop learn its successor's address
  # WITHOUT a Terraform dependency cycle. The ring is a cycle by construction
  # (Tokyo -> Oregon), so deriving URLs from the project number instead of from
  # resource attributes is what makes a single-pass apply possible at all.
  # verify.sh asserts these match the URLs Google actually assigned.
  urls = {
    for r in local.ring :
    r => "https://ratw-${r}-${data.google_project.this.number}.${r}.run.app"
  }

  # Every hop can dial every other hop's address, but only its designated successor
  # is reachable: IAM grants run.invoker on exactly one peer (see module/hop).
  peers = join(",", [for r in local.ring : "${r}=${local.urls[r]}"])

  origin = local.ring[0]
  # The last hop is the only caller permitted to close the ring.
  closer = local.ring[length(local.ring) - 1]
}

data "google_project" "this" {
  project_id = var.project_id
}
