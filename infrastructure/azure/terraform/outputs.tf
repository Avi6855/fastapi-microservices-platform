output "resource_group_name" {
  value       = azurerm_resource_group.this.name
  description = "Resource group holding every platform resource."
}

output "acr_login_server" {
  value       = azurerm_container_registry.this.login_server
  description = "Registry host for the Helm images.repositoryPrefix value."
}

output "key_vault_uri" {
  value       = azurerm_key_vault.this.vault_uri
  description = "Key Vault URI for runtime secret projection."
}

output "oidc_issuer_url" {
  value       = azurerm_kubernetes_cluster.this.oidc_issuer_url
  description = "AKS OIDC issuer used by the federated credential."
}

output "workload_identity_client_id" {
  value       = azurerm_user_assigned_identity.runtime.client_id
  description = "Client ID for the foundation chart's service account annotation."
}

output "aks_cluster_name" {
  value       = azurerm_kubernetes_cluster.this.name
  description = "Cluster name for az aks get-credentials."
}

output "media_blob_endpoint" {
  value       = azurerm_storage_account.media.primary_blob_endpoint
  description = "Blob endpoint for MEDIA_AZURE_STORAGE_ACCOUNT_URL."
}

output "media_storage_account_name" {
  value       = azurerm_storage_account.media.name
  description = "Storage account name for MEDIA_AZURE_STORAGE_ACCOUNT_NAME."
}
