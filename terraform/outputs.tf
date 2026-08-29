# NOTE: the origin URL is intentionally NOT written to any file that git tracks.
# Terraform state is gitignored; read it with `terraform output -raw origin_url`.

output "origin_url" {
  value       = module.hop[local.origin].uri
  description = "Public ingress. Keep out of source control."
}

# True once pass 2 has wired every hop to the URL Cloud Run actually assigned.
# verify-deploy.sh refuses to run while this is false.
output "peer_map_wired" {
  value = alltrue(concat(
    [for r in local.ring : module.hop[r].uri == local.urls[r]],
    [google_cloudfunctions2_function.web.service_config[0].uri == local.web_url],
  ))
}

# Consumed by scripts/deploy.sh to generate peers.auto.tfvars.
output "actual_urls" {
  value = { for r in local.ring : r => module.hop[r].uri }
}

output "buckets" {
  value = { for r in local.ring : r => module.hop[r].bucket }
}

output "service_accounts" {
  value = { for r in local.ring : r => module.hop[r].service_account }
}

output "web_url" {
  value       = google_cloudfunctions2_function.web.service_config[0].uri
  description = "The page to open. Keep out of source control."
}



output "project_number" {
  value       = data.google_project.this.number
  description = "Used by verify-deploy.sh to derive hop URLs without printing them."
}
