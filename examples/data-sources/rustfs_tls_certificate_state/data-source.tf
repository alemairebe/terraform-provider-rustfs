data "rustfs_tls_certificate_state" "current" {}

output "tls_certificate_fingerprint" {
  value = data.rustfs_tls_certificate_state.current.certificate_fingerprint
}

output "tls_certificate_not_after" {
  value = data.rustfs_tls_certificate_state.current.not_after_unix
}
