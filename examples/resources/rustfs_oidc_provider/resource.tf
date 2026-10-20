resource "rustfs_oidc_provider" "keycloak" {
  id            = "keycloak"
  config_url    = "https://kc.example.com/realms/rustfs/.well-known/openid-configuration"
  client_id     = "rustfs"
  client_secret = var.oidc_client_secret
  display_name  = "Keycloak"
  scopes        = ["openid", "profile", "email"]
}

variable "oidc_client_secret" {
  type      = string
  sensitive = true
}

output "oidc_restart_required" {
  value = rustfs_oidc_provider.keycloak.restart_required
}
