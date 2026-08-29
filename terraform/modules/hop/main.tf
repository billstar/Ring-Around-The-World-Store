# One region of the ring: a regional bucket, a dedicated service account, and the
# Cloud Run function. Deployed six times with different variables and nothing else.

terraform {
  required_providers {
    google = { source = "hashicorp/google" }
  }
}

# Regional, not multi-region. Regionality is the entire point of the project.
resource "google_storage_bucket" "hop" {
  name                        = var.bucket_name
  project                     = var.project_id
  location                    = var.region
  storage_class               = "STANDARD"
  uniform_bucket_level_access = true
  public_access_prevention    = "enforced"
  force_destroy               = true # a demo; destroy must not require manual emptying

  lifecycle_rule {
    condition { age = 7 }
    action { type = "Delete" }
  }
}

# Each hop runs as its own identity, so "which region wrote this object" is an
# IAM fact, not a claim in the payload.
resource "google_service_account" "hop" {
  account_id   = "ratw-${var.region}"
  display_name = "RATW hop ${var.region}"
  project      = var.project_id
}

# Least privilege: this hop can write ONLY its own regional bucket.
resource "google_storage_bucket_iam_member" "own_bucket" {
  bucket = google_storage_bucket.hop.name
  role   = "roles/storage.objectAdmin"
  member = "serviceAccount:${google_service_account.hop.email}"
}

resource "google_project_iam_member" "log_writer" {
  project = var.project_id
  role    = "roles/logging.logWriter"
  member  = "serviceAccount:${google_service_account.hop.email}"
}

resource "google_cloudfunctions2_function" "hop" {
  name     = "ratw-${var.region}"
  project  = var.project_id
  location = var.region

  build_config {
    runtime     = var.runtime
    entry_point = "Ring"
    source {
      storage_source {
        bucket = var.source_bucket
        object = var.source_object
      }
    }
  }

  service_config {
    max_instance_count = var.max_instances
    min_instance_count = 0 # scale to zero: idle cost is storage only
    available_memory   = "256Mi"
    timeout_seconds    = var.deadline_sec + 30

    # The origin serves /ring (public) and /close (OIDC-checked in-handler), so it
    # must be reachable by anyone; every other hop is gated by IAM invoker bindings.
    ingress_settings               = "ALLOW_ALL"
    all_traffic_on_latest_revision = true
    service_account_email          = google_service_account.hop.email

    environment_variables = merge({
      RATW_REGION       = var.region
      RATW_BUCKET       = google_storage_bucket.hop.name
      RATW_PEERS        = var.peers
      RATW_RING         = var.ring
      RATW_IS_ORIGIN    = var.is_origin ? "true" : "false"
      RATW_DEADLINE_SEC = tostring(var.deadline_sec)
      RATW_LOCAL        = "false"
      }, var.is_origin ? {
      RATW_SELF_URL       = var.self_url
      RATW_CLOSER_SA      = var.closer_sa
      RATW_ALLOWED_ORIGIN = var.allowed_origin
    } : {})
  }
}
