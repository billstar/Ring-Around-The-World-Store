# ---------------------------------------------------------------------------
# Source archive. cmd/ and test files are excluded: the buildpack builds the root
# package, and a second main package in the tree is an unnecessary way to confuse it.
# ---------------------------------------------------------------------------

resource "random_id" "suffix" {
  byte_length = 3
}

resource "google_storage_bucket" "source" {
  name                        = "ratw-source-${random_id.suffix.hex}"
  project                     = var.project_id
  location                    = var.origin_region
  uniform_bucket_level_access = true
  public_access_prevention    = "enforced"
  force_destroy               = true
}

data "archive_file" "source" {
  type        = "zip"
  source_dir  = "${path.module}/../functions"
  output_path = "${path.module}/.build/functions.zip"
  excludes    = ["cmd/**", "**/*_test.go", ".build/**"]
}

# The hash in the object name is what makes `terraform apply` redeploy code changes:
# a new hash is a new object, which is a new build.
resource "google_storage_bucket_object" "source" {
  name   = "functions-${data.archive_file.source.output_md5}.zip"
  bucket = google_storage_bucket.source.name
  source = data.archive_file.source.output_path
}

# ---------------------------------------------------------------------------
# The six hops.
# ---------------------------------------------------------------------------

module "hop" {
  for_each = toset(local.ring)
  source   = "./modules/hop"

  project_id    = var.project_id
  region        = each.key
  bucket_name   = "ratw-${each.key}-${random_id.suffix.hex}"
  source_bucket = google_storage_bucket.source.name
  source_object = google_storage_bucket_object.source.name
  peers         = local.peers
  deadline_sec  = local.deadlines[each.key]
  runtime       = var.runtime
  max_instances = var.max_instances

  is_origin = each.key == local.origin
  self_url  = each.key == local.origin ? local.urls[local.origin] : ""
  # Only Tokyo's identity may close the ring; /close checks this claim in-handler.
  closer_sa      = each.key == local.origin ? "ratw-${local.closer}@${var.project_id}.iam.gserviceaccount.com" : ""
  allowed_origin = each.key == local.origin ? var.allowed_web_origin : ""
}

# ---------------------------------------------------------------------------
# Invoker IAM, kept at the root so it can depend on ALL hops existing. The ring is
# a cycle, so these bindings cannot live inside the module without a dependency loop.
#
# Each hop may invoke exactly one successor. Tokyo may invoke the origin (for /close).
# ---------------------------------------------------------------------------

locals {
  # region -> the region it is allowed to call
  successor = {
    for i, r in local.ring :
    r => local.ring[(i + 1) % length(local.ring)]
  }
}

resource "google_cloud_run_service_iam_member" "successor_invoker" {
  for_each = local.successor

  project  = var.project_id
  location = each.value                                  # the callee's region
  service  = module.hop[each.value].service_name         # the callee
  role     = "roles/run.invoker"
  member   = "serviceAccount:${module.hop[each.key].service_account}" # the caller
}

# The origin's /ring must be reachable by a browser. This is the only public grant,
# and it is why /close performs its own OIDC check rather than relying on IAM.
resource "google_cloud_run_service_iam_member" "public_ingress" {
  project  = var.project_id
  location = local.origin
  service  = module.hop[local.origin].service_name
  role     = "roles/run.invoker"
  member   = "allUsers"
}
