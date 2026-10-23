variable "prefix" {
  type        = string
  description = "Short workload prefix used in every resource name."
  default     = "fastapi"

  validation {
    condition     = can(regex("^[a-z][a-z0-9]{2,11}$", var.prefix))
    error_message = "prefix must be 3-12 lowercase alphanumeric characters beginning with a letter."
  }
}

variable "environment" {
  type        = string
  description = "Deployment environment name."
  default     = "prod"

  validation {
    condition     = can(regex("^[a-z][a-z0-9]{2,11}$", var.environment))
    error_message = "environment must be 3-12 lowercase alphanumeric characters beginning with a letter."
  }
}

variable "location" {
  type        = string
  description = "Azure region for every resource."
  default     = "centralindia"
}

variable "kubernetes_version" {
  type        = string
  description = "AKS control-plane version. Must satisfy the chart's kubeVersion floor; lookup the region's supported versions before the first plan."
  default     = "1.34"
}

variable "node_vm_size" {
  type        = string
  description = "System node pool VM size."
  default     = "Standard_D4s_v5"
}

variable "node_min_count" {
  type        = number
  description = "Minimum system node count for cluster autoscaling."
  default     = 2

  validation {
    condition     = var.node_min_count >= 1 && var.node_min_count <= 5
    error_message = "node_min_count must be between 1 and 5."
  }
}

variable "node_max_count" {
  type        = number
  description = "Maximum system node count for cluster autoscaling."
  default     = 5

  validation {
    condition     = var.node_max_count >= 2 && var.node_max_count <= 20
    error_message = "node_max_count must be between 2 and 20."
  }
}

variable "kubernetes_namespace" {
  type        = string
  description = "Namespace of the Helm releases; part of the federated credential subject."
  default     = "fastapi-platform"
}

variable "service_account_name" {
  type        = string
  description = "Runtime service account of the foundation release; part of the federated credential subject."
  default     = "platform-runtime"
}

variable "tags" {
  type        = map(string)
  description = "Tags applied to every resource."
  default = {
    workload = "fastapi-microservices-platform"
  }
}
