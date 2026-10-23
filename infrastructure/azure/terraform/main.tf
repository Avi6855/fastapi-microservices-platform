locals {
  base      = lower(format("%s-%s", var.prefix, var.environment))
  compact   = replace(local.base, "-", "")
  suffix    = random_string.suffix.result
  aks_name  = "aks-${local.base}"
  acr_name  = substr("acr${local.compact}${local.suffix}", 0, 50)
  kv_name   = substr("kv-${local.compact}-${local.suffix}", 0, 24)
  st_name   = substr("st${local.compact}${local.suffix}", 0, 24)
  identity  = "id-${local.base}-runtime"
  federated = "fc-${local.base}-runtime"
  container = "fastapi-platform-media"
}

data "azurerm_client_config" "current" {}

resource "random_string" "suffix" {
  length  = 5
  special = false
  upper   = false
}

resource "azurerm_resource_group" "this" {
  name     = "rg-${local.base}"
  location = var.location
  tags     = var.tags
}

resource "azurerm_container_registry" "this" {
  name                = local.acr_name
  resource_group_name = azurerm_resource_group.this.name
  location            = azurerm_resource_group.this.location
  sku                 = "Standard"
  # Never enable the registry administrator: cluster nodes pull with their
  # kubelet identity, and pipelines push with a federated service principal.
  admin_enabled = false
  tags          = var.tags
}

resource "azurerm_kubernetes_cluster" "this" {
  name                = local.aks_name
  resource_group_name = azurerm_resource_group.this.name
  location            = azurerm_resource_group.this.location
  dns_prefix          = local.aks_name
  kubernetes_version  = var.kubernetes_version
  sku_tier            = "Standard"
  tags                = var.tags

  # Workload identity needs both the issuer and the webhook.
  oidc_issuer_enabled       = true
  workload_identity_enabled = true

  key_vault_secrets_provider {
    secret_rotation_enabled = true
  }

  default_node_pool {
    name                 = "system"
    vm_size              = var.node_vm_size
    auto_scaling_enabled = true
    min_count            = var.node_min_count
    max_count            = var.node_max_count
    orchestrator_version = var.kubernetes_version
  }

  identity {
    type = "SystemAssigned"
  }

  network_profile {
    network_plugin = "azure"
    network_policy = "calico"
    outbound_type  = "loadBalancer"
  }
}

resource "azurerm_key_vault" "this" {
  name                       = local.kv_name
  resource_group_name        = azurerm_resource_group.this.name
  location                   = azurerm_resource_group.this.location
  tenant_id                  = data.azurerm_client_config.current.tenant_id
  sku_name                   = "standard"
  purge_protection_enabled   = true
  soft_delete_retention_days = 7
  rbac_authorization_enabled = true
  tags                       = var.tags
}

resource "azurerm_storage_account" "media" {
  name                     = local.st_name
  resource_group_name      = azurerm_resource_group.this.name
  location                 = azurerm_resource_group.this.location
  account_tier             = "Standard"
  account_replication_type = "ZRS"
  min_tls_version          = "TLS1_2"
  tags                     = var.tags

  blob_properties {
    versioning_enabled = true
    delete_retention_policy {
      days = 7
    }
    container_delete_retention_policy {
      days = 7
    }
  }

  # Public network access stays enabled because the module creates no private
  # endpoint and no service endpoint. A hardened promotion narrows this rule to
  # the cluster subnet together with a private endpoint; see the runbook.
  network_rules {
    default_action = "Allow"
    bypass         = ["AzureServices"]
  }
}

resource "azurerm_storage_container" "media" {
  name                  = local.container
  storage_account_id    = azurerm_storage_account.media.id
  container_access_type = "private"
}

resource "azurerm_user_assigned_identity" "runtime" {
  name                = local.identity
  resource_group_name = azurerm_resource_group.this.name
  location            = azurerm_resource_group.this.location
  tags                = var.tags
}

resource "azurerm_federated_identity_credential" "runtime" {
  name                = local.federated
  resource_group_name = azurerm_resource_group.this.name
  parent_id           = azurerm_user_assigned_identity.runtime.id
  audience            = ["api://AzureADTokenExchange"]
  issuer              = azurerm_kubernetes_cluster.this.oidc_issuer_url
  subject             = "system:serviceaccount:${var.kubernetes_namespace}:${var.service_account_name}"
}

resource "azurerm_role_assignment" "kubelet_acr_pull" {
  scope                = azurerm_container_registry.this.id
  role_definition_name = "AcrPull"
  principal_id         = azurerm_kubernetes_cluster.this.kubelet_identity[0].object_id
}

resource "azurerm_role_assignment" "runtime_acr_pull" {
  scope                = azurerm_container_registry.this.id
  role_definition_name = "AcrPull"
  principal_id         = azurerm_user_assigned_identity.runtime.principal_id
}

resource "azurerm_role_assignment" "runtime_key_vault_secrets" {
  scope                = azurerm_key_vault.this.id
  role_definition_name = "Key Vault Secrets User"
  principal_id         = azurerm_user_assigned_identity.runtime.principal_id
}

resource "azurerm_role_assignment" "runtime_blob_data" {
  scope                = azurerm_storage_account.media.id
  role_definition_name = "Storage Blob Data Contributor"
  principal_id         = azurerm_user_assigned_identity.runtime.principal_id
}

