# ---------------------------------------------------------------------------
# ratw-web: the public client. Serves the SPA and proxies Cloud Logging queries.
# Not a hop — it holds no bucket permissions and writes no receipts.
# ---------------------------------------------------------------------------

data "archive_file" "web" {
  type        = "zip"
  source_dir  = "${path.module}/../web"
  output_path = "${path.module}/.build/web.zip"
  excludes    = ["node_modules/**", "dist/**", "public/config.js"]
}

resource "google_storage_bucket_object" "web" {
  name   = "web-${data.archive_file.web.output_md5}.zip"
  bucket = google_storage_bucket.source.name
  source = data.archive_file.web.output_path
}

resource "google_service_account" "web" {
  account_id   = "ratw-web"
  display_name = "RATW web client"
  project      = var.project_id
}

# Read-only, and only logs. The whole reason /logs is a server-side proxy is so this
# credential never has to exist in a browser.
resource "google_project_iam_member" "web_log_viewer" {
  project = var.project_id
  role    = "roles/logging.viewer"
  member  = "serviceAccount:${google_service_account.web.email}"
}

resource "google_cloudfunctions2_function" "web" {
  name     = "ratw-web"
  project  = var.project_id
  location = var.origin_region

  build_config {
    runtime     = "nodejs22"
    entry_point = "Web"
    source {
      storage_source {
        bucket = google_storage_bucket.source.name
        object = google_storage_bucket_object.web.name
      }
    }
  }

  service_config {
    max_instance_count             = var.max_instances
    min_instance_count             = 0
    available_memory               = "256Mi"
    timeout_seconds                = 60
    ingress_settings               = "ALLOW_ALL"
    all_traffic_on_latest_revision = true
    service_account_email          = google_service_account.web.email

    # The origin URL reaches the browser via /config.js at runtime, never via a
    # tracked file.
    environment_variables = {
      RATW_ORIGIN_URL = local.urls[local.origin]
    }
  }
}

resource "google_cloud_run_service_iam_member" "web_public" {
  project  = var.project_id
  location = var.origin_region
  service  = google_cloudfunctions2_function.web.name
  role     = "roles/run.invoker"
  member   = "allUsers"
}
