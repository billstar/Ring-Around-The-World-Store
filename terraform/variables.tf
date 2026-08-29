variable "project_id" {
  type    = string
  default = "ring-around-the-world-store"
}

variable "origin_region" {
  type        = string
  default     = "us-west1"
  description = "Public ingress and ring-close region."
}

# Bounds the blast radius of a public, unauthenticated /ring endpoint. Each call
# fans out to seven writes across six regions, so an unbounded scanner would be an
# amplifier. This ceiling caps cost regardless of who finds the URL.
variable "max_instances" {
  type    = number
  default = 3
}

variable "runtime" {
  type    = string
  default = "go125"
  # Google retires older runtimes for NEW deployments without notice here; go123 was
  # already refused. `gcloud functions runtimes list` is the source of truth.
}

variable "allowed_web_origin" {
  type        = string
  default     = null
  description = <<-EOT
    Exact browser origin allowed to call /ring. Defaults to the deployed ratw-web URL,
    which is deterministic and therefore known before either service exists. Never "*".
  EOT
}

variable "asia_region" {
  type    = string
  default = "asia-east1"
  # asia-northeast1 (Tokyo) is the natural choice and is listed as available, but this
  # project's per-region initialization quota there is zero, so Cloud Run refuses to
  # initialize. Any allowlisted Asian region satisfies the requirement; the Go
  # allowlist carries asia-northeast1, asia-east1 and asia-southeast1 so this is a
  # one-line change if a quota increase later lands.
}

variable "enable_asia_hop" {
  type        = bool
  default     = false
  description = <<-EOT
    Adds the sixth (Asia) region. Requires a Cloud Run region-initialization quota
    increase: this project's limit is 5 regions, and the 6th is refused in any region.
    Request via console.cloud.google.com/iam-admin/quotas (service: Cloud Run Admin API),
    then set this to true and re-apply.
  EOT
}

variable "peer_urls" {
  type        = map(string)
  default     = {}
  description = <<-EOT
    region -> real Cloud Run URL. Empty on the first apply; written to
    peers.auto.tfvars by scripts/deploy.sh and supplied on the second pass.
    Gitignored: these are the public URLs and must not enter source control.
  EOT
}

variable "web_url" {
  type        = string
  default     = ""
  description = "Real ratw-web URL, supplied on the second pass (see peer_urls)."
}
