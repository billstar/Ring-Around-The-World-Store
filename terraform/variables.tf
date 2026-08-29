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
  default = "go123"
}

variable "allowed_web_origin" {
  type        = string
  default     = "*"
  description = <<-EOT
    Exact origin allowed to call /ring from a browser. Defaults to "*" only so the
    first apply can succeed before ratw-web exists; the web-tier apply narrows it to
    the real origin. Never leave this as "*" once the client is deployed.
  EOT
}
