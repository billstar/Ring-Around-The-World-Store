# NOTE: the origin URL is intentionally NOT written to any file that git tracks.
# Terraform state is gitignored; read it with `terraform output -raw origin_url`.

output "origin_url" {
  value       = module.hop[local.origin].uri
  description = "Public ingress. Keep out of source control."
}

output "expected_urls_match" {
  description = "Asserts Cloud Run assigned the deterministic URLs the peer map assumes."
  value = alltrue([
    for r in local.ring : module.hop[r].uri == local.urls[r]
  ])
}

output "buckets" {
  value = { for r in local.ring : r => module.hop[r].bucket }
}

output "service_accounts" {
  value = { for r in local.ring : r => module.hop[r].service_account }
}
