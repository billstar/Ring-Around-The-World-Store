output "uri" { value = google_cloudfunctions2_function.hop.service_config[0].uri }
output "service_account" { value = google_service_account.hop.email }
output "service_name" { value = google_cloudfunctions2_function.hop.name }
output "bucket" { value = google_storage_bucket.hop.name }
