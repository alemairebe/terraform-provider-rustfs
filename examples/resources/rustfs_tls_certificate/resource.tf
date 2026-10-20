# Pass the PEM pair from your certificate source, for example
# certificate_pem = file("certs/api.crt") and key_pem = file("certs/api.key").
variable "certificate_pem" {
  description = "PEM-encoded leaf certificate chain to publish"
  type        = string
}

variable "key_pem" {
  description = "PEM-encoded private key for the leaf certificate"
  type        = string
  sensitive   = true
}

resource "rustfs_tls_certificate" "api" {
  certificate_pem = var.certificate_pem
  key_pem         = var.key_pem
}

output "tls_certificate_fingerprint" {
  value = rustfs_tls_certificate.api.certificate_fingerprint
}
