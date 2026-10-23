# Runbook — Azure AKS delivery

Operates the target defined in
[ADR-043](../adr/ADR-043-azure-aks-delivery-target.md). The module is authored and
statically validated; it has never been applied and no resource exists behind it.
Treat every step below as a rehearsal until an owned subscription exists.

## Scope

- `infrastructure/azure/terraform` — AKS, ACR, Key Vault, blob storage, workload
  identity and its role assignments.
- `infrastructure/helm/*/values-aks.yaml` — delivery overlays for the two charts.
- `.github/workflows/azure-delivery.yml` — manual validation and plan rehearsal.

Out of scope: ingress, DNS, certificates, private endpoints, evidence of an
applied deployment, and image publication to ACR.

## What does not exist

| Claim | Reality |
|---|---|
| A running AKS cluster | No cluster has been created |
| Images in ACR | The registry does not exist; digests in `values-aks.yaml` are placeholders |
| Secrets in Key Vault | The vault does not exist; the runtime Secret is still operator-supplied |
| A passing `terraform plan` against a subscription | Never executed |
| A working `helm upgrade` against a cluster | Never executed |

If asked whether this platform runs on Azure, the answer is no. The adapters and
the target are reviewable and lint-clean; the deployment is not.

## Prerequisites for a real rehearsal

1. An owned subscription with permission to create AKS, ACR, Key Vault, storage,
   and role assignments.
2. `terraform` 1.9 or later, `azure-cli`, `helm` 4.2.4 or later, `kubectl`.
3. A registered federated credential for the pipeline, exposed as the repository
   variables `AZURE_CLIENT_ID`, `AZURE_TENANT_ID`, and `AZURE_SUBSCRIPTION_ID`.
   No client secret is created or stored.
4. A remote state backend. This module intentionally configures none, so a real
   apply must add one first; without it, the plan job's local state is discarded
   and cannot be applied later.

## Validation (safe, no subscription)

```bash
terraform -chdir=infrastructure/azure/terraform init -backend=false -input=false
terraform -chdir=infrastructure/azure/terraform validate
terraform -chdir=infrastructure/azure/terraform fmt -check -recursive

helm lint infrastructure/helm/fastapi-platform-foundation --strict \
  --values infrastructure/helm/fastapi-platform-foundation/values.yaml \
  --values infrastructure/helm/fastapi-platform-foundation/values-aks.yaml
helm lint infrastructure/helm/fastapi-platform --strict \
  --values infrastructure/helm/fastapi-platform/values.yaml \
  --values infrastructure/helm/fastapi-platform/values-aks.yaml
```

## Plan and apply rehearsal

```bash
cp infrastructure/azure/terraform/terraform.tfvars.example \
   infrastructure/azure/terraform/terraform.tfvars
terraform -chdir=infrastructure/azure/terraform plan -out=tfplan
terraform -chdir=infrastructure/azure/terraform show -no-color tfplan | head -200
```

Before an apply, resolve the three known gaps:

1. **Cluster version.** `kubernetes_version` must be offered by the region and
   satisfy the charts' `kubeVersion` floor. A mismatch fails at `helm upgrade`,
   not at plan.
2. **Image supply.** Publish the scanned images to the ACR named by
   `terraform output acr_login_server` and replace every placeholder digest in
   `values-aks.yaml` with the immutable digest of that build.
3. **Runtime Secret.** Create the Kubernetes Secret referenced by
   `runtime.secretName` from Key Vault entries, or project it with the Key Vault
   CSI driver. The repository does not create it, and no generated password is
   stored in state.

Then install foundation, then the application chart, then verify:

```bash
az aks get-credentials --resource-group "$(terraform -chdir=infrastructure/azure/terraform output -raw resource_group_name)" \
  --name "$(terraform -chdir=infrastructure/azure/terraform output -raw aks_cluster_name)"

helm upgrade --install platform-foundation infrastructure/helm/fastapi-platform-foundation \
  --namespace fastapi-platform --create-namespace \
  --values infrastructure/helm/fastapi-platform-foundation/values.yaml \
  --values infrastructure/helm/fastapi-platform-foundation/values-aks.yaml

kubectl --namespace fastapi-platform get serviceaccount platform-runtime \
  --output jsonpath='{.metadata.annotations.azure\.workload\.identity/client-id}'

helm upgrade --install fastapi-platform infrastructure/helm/fastapi-platform \
  --namespace fastapi-platform \
  --values infrastructure/helm/fastapi-platform/values.yaml \
  --values infrastructure/helm/fastapi-platform/values-aks.yaml

kubectl --namespace fastapi-platform rollout status deployment/reference-service
kubectl --namespace fastapi-platform get pods --show-labels | grep azure.workload.identity
```

## Detection and symptoms

| Symptom | Likely cause |
|---|---|
| Pods in `ErrImagePull` from `*.azurecr.io` | Missing `AcrPull` on the kubelet identity, or an unpublished digest |
| `AADSTS` error in a pod log | Federated subject mismatch: namespace or service account name differs from the Terraform variables |
| Media uploads fail with `403` from Blob | The account key is absent from the runtime Secret, or the network rule blocks cluster egress |
| `helm upgrade` fails on `kubeVersion` | Cluster version below the chart floor |
| `terraform plan` shows a replacement for the cluster | An immutable AKS property changed such as network plugin |

## Impact

The target is not in the request path, so no user-visible behavior depends on it
today. Breaking it breaks the rehearsal only.

## Safe mitigation and recovery

- Wrong identity wiring: fix the Terraform variables and re-plan; do not
  hand-grant roles in the portal, because the next plan would not reproduce them.
- Failed rollout: `helm rollback fastapi-platform` restores the previous revision;
  the previous revision is only meaningful after a first successful install.
- Storage lockout: restore the intended network rule and verify with
  `az storage account show --query networkRuleSet`.

## Escalation

Escalate when a plan succeeds but the workload cannot reach Blob or Key Vault:
that is a network or identity design gap, and it needs an ADR change rather than
an in-place portal edit.

## Follow-up

- Add a remote state backend and a reviewed ACR publication job before the first
  real apply.
- Narrow the storage network rule with a cluster subnet and private endpoint.
- Add an ingress controller, certificate issuer, and DNS record so the target can
  serve a real origin.
