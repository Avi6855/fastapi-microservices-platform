# ADR-043 — Azure AKS delivery target

- Status: `Accepted`
- Date: `2026-10-23`
- Owners: platform architecture
- Supersedes: none
- Superseded by: none

## Context

The platform already delivers as raw Kubernetes manifests and two Helm charts,
and CI proves that path in a disposable `Kind` cluster
([ADR-028](ADR-028-kubernetes-conformance-ci.md)). What it does not have is a
cloud target: there is no infrastructure definition for a managed cluster, no
registry owned by the platform, and no identity-based access to secrets.

[ADR-042](ADR-042-azure-blob-object-storage-adapter.md) added an Azure Blob
adapter but verified it only against Azurite. The remaining question is what a
real Azure deployment would require, expressed as reviewable configuration
rather than a diagram.

## Decision

Add an Azure delivery target in three layers:

1. `infrastructure/azure/terraform` defines the resource group, AKS, Azure
   Container Registry, Key Vault, a blob storage account with versioning and
   delete retention, a user-assigned managed identity, and the federated
   credential that binds it to `system:serviceaccount:<namespace>:<service
   account>`. The identity receives only `AcrPull`, `Key Vault Secrets User`, and
   `Storage Blob Data Contributor`; the registry administrator account stays
   disabled and cluster nodes pull with their kubelet identity.
2. Two Helm overlays, `values-aks.yaml`, adapt the existing charts: images come
   from the registry named by the Terraform output, no image-pull Secret exists,
   pods opt in to workload identity, and Media selects the Azure Blob adapter.
   The charts gained `podLabels`, `podAnnotations`, and `serviceAccount.
   annotations` for this, defaulting to empty so the portable delivery is
   unchanged.
3. `.github/workflows/azure-delivery.yml` validates Terraform and renders both
   overlays on demand, and can run `terraform plan` with a federated `azure/login`
   that uses no client secret. The default `quality` job additionally renders the
   Azure overlays so they cannot rot unnoticed.

Terraform is chosen over Bicep because `terraform validate` checks every argument
against the provider schema offline, which is stronger static evidence than a
syntax-only template compile. The repository therefore has one IaC toolchain, not
two.

## Consequences

### Positive

- The Azure target is reviewable configuration with a pinned provider lock file,
  not a description in prose.
- Identity is federated: nothing in the repository or the workflow needs a client
  secret, and the registry administrator is disabled.
- The reusable chart features are generic (`podLabels`, `podAnnotations`,
  `serviceAccount.annotations`), so they also serve any other cloud's workload
  identity.

### Negative and risks

- Two deployment paths now exist for one workload. They share the charts, which
  limits drift, but only the `Kind` path is executed by CI.
- Terraform has no remote backend in this repository, because no owned storage
  account exists for state. A real promotion adds one before the first apply; the
  plan job therefore uses disposable local state and cannot be followed by an
  apply.
- The storage network rule allows public access in this module. Restricting it
  needs a cluster subnet and a private endpoint, which the module does not create.

## Compatibility and migration

The portable delivery is unaffected: `podLabels`, `podAnnotations`, and
`serviceAccount.annotations` default to empty, the conformance values render the
same resources, the immutable-digest rule still fails a digest-less render, and
the `Kind` conformance path keeps running in CI. No contract, schema, or service
interface changes with this target.

A promotion moves in one direction and uses immutable inputs:

1. Apply the Terraform module and record the outputs.
2. Publish the already-scanned images to the registry and replace every
   placeholder digest in `values-aks.yaml`.
3. Set the service account client ID and the storage endpoint from the outputs.
4. Install foundation, then the application chart, then verify rollout and pod
   identity labels.

Rolling back before a first successful install means deleting the resource group;
the repository holds no state, so there is nothing to reconcile. After a real
install, rollback is a Helm revision plus the infrastructure change that owns it.
The module is additive: it does not modify the Compose or `Kind` paths.

## Boundaries

- **This module has never been applied.** No subscription, cluster, registry, key
  vault, identity, or storage account exists behind it. Every digest in
  `values-aks.yaml` is a sixty-four-zero placeholder, and the service account
  annotation and storage endpoint are explicit `replace-with-...` values.
- `terraform plan` has not run against a subscription either. The unexecuted
  workflow is a rehearsal of the promotion path, not evidence of a deployment.
- The AKS version is an input. It must satisfy the charts' `kubeVersion` floor and
  be offered by the chosen region; this ADR does not claim a supported pairing.
- No ingress controller, certificate issuer, DNS, private endpoint, backup
  policy, or monitoring workspace is part of this target. Those remain the
  deployment gaps already listed in the reviewer guide.
- Images are still built and scanned by the existing `GHCR` job. Publishing them
  to `ACR` is deliberately not automated; a promotion would add a separate,
  reviewed job.

## Alternatives considered

- Bicep or ARM templates: rejected for the weaker offline validation described
  above, not because of any Azure capability difference.
- AKS Automatic with a default node pool and no identity wiring: rejected because
  it hides the federated-credential and role-assignment decisions this ADR is
  meant to make reviewable.
- Container Apps instead of AKS: rejected because the workload already ships
  Kubernetes resources and Helm packaging; changing the scheduler would be a
  separate decision with a much larger blast radius.

## Validation

Executed while authoring this decision:

```bash
terraform -chdir=infrastructure/azure/terraform init -backend=false -input=false
terraform -chdir=infrastructure/azure/terraform fmt -check -recursive
terraform -chdir=infrastructure/azure/terraform validate

helm lint infrastructure/helm/fastapi-platform-foundation --strict \
  --values infrastructure/helm/fastapi-platform-foundation/values.yaml \
  --values infrastructure/helm/fastapi-platform-foundation/values-aks.yaml
helm lint infrastructure/helm/fastapi-platform --strict \
  --values infrastructure/helm/fastapi-platform/values.yaml \
  --values infrastructure/helm/fastapi-platform/values-aks.yaml
helm template fastapi-platform infrastructure/helm/fastapi-platform \
  --namespace fastapi-platform \
  --values infrastructure/helm/fastapi-platform/values.yaml \
  --values infrastructure/helm/fastapi-platform/values-aks.yaml
```

`validate` reports a valid configuration, both charts lint and render, the
rendered pods carry `azure.workload.identity/use: "true"`, images reference the
`azurecr.io` prefix, `ghcr.io` appears zero times, and the conformance renders
are byte-for-byte unaffected by the new chart values. None of this is deployment
evidence.

## Related material

- [Azure AKS delivery runbook](../runbooks/azure-aks-delivery.md)
- [ADR-042 — Azure Blob adapter](ADR-042-azure-blob-object-storage-adapter.md)
- [ADR-027 — raw Kubernetes delivery baseline](ADR-027-raw-kubernetes-delivery-baseline.md)
- [ADR-029 — Helm packaging and controlled migrations](ADR-029-helm-packaging-and-controlled-migrations.md)
- [Kubernetes deployment runbook](../runbooks/kubernetes-deployment.md)
