variable "project_id" { type = string }
variable "region" { type = string }
variable "bucket_name" { type = string }
variable "source_bucket" { type = string }
variable "source_object" { type = string }
variable "peers" { type = string }
variable "deadline_sec" { type = number }
variable "runtime" { type = string }
variable "max_instances" { type = number }

variable "is_origin" {
  type    = bool
  default = false
}

variable "self_url" {
  type    = string
  default = ""
}

variable "closer_sa" {
  type    = string
  default = ""
}

variable "allowed_origin" {
  type    = string
  default = ""
}
