data "rustfs_oidc_config" "all" {}

data "rustfs_oidc_config" "keycloak" {
  id = "keycloak"
}

output "oidc_provider_ids" {
  value = [for provider in data.rustfs_oidc_config.all.providers : provider.provider_id]
}

output "keycloak_client_secret_configured" {
  value = data.rustfs_oidc_config.keycloak.providers[0].client_secret_configured
}
