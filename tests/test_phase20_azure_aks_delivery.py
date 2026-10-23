"""Invariants for the Azure delivery target.

The target is authored configuration: these tests protect its shape and the
honesty of its claims, not a running deployment. The authoritative statements
live in ADR-043.
"""

from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
TERRAFORM = ROOT / "infrastructure" / "azure" / "terraform"
FOUNDATION = ROOT / "infrastructure" / "helm" / "fastapi-platform-foundation"
PLATFORM = ROOT / "infrastructure" / "helm" / "fastapi-platform"
WORKFLOW = ROOT / ".github" / "workflows" / "azure-delivery.yml"


def _terraform_text() -> str:
    return "\n".join(path.read_text(encoding="utf-8") for path in TERRAFORM.glob("*.tf"))


def test_azure_target_keeps_identity_federated_and_uncredentialed() -> None:
    main = (TERRAFORM / "main.tf").read_text(encoding="utf-8")

    assert "oidc_issuer_enabled       = true" in main
    assert "workload_identity_enabled = true" in main
    assert 'audience            = ["api://AzureADTokenExchange"]' in main
    assert (
        'subject             = "system:serviceaccount:${var.kubernetes_namespace}:'
        '${var.service_account_name}"' in main
    )
    assert "admin_enabled = false" in main
    assert "client_secret" not in _terraform_text()
    assert "AZURE_CLIENT_SECRET" not in _terraform_text()


def test_azure_target_grants_only_the_roles_the_platform_needs() -> None:
    main = (TERRAFORM / "main.tf").read_text(encoding="utf-8")

    for role in ("AcrPull", "Key Vault Secrets User", "Storage Blob Data Contributor"):
        assert f'role_definition_name = "{role}"' in main
    assert "kubelet_identity[0].object_id" in main
    assert "rbac_authorization_enabled" in main
    assert "purge_protection_enabled" in main


def test_azure_target_never_commits_state() -> None:
    gitignore = (ROOT / ".gitignore").read_text(encoding="utf-8")

    assert "**/.terraform/" in gitignore
    assert "*.tfstate" in gitignore
    assert list(TERRAFORM.glob("*.tfstate")) == []
    assert (TERRAFORM / ".terraform.lock.hcl").is_file()
    assert "required_version" in (TERRAFORM / "versions.tf").read_text(encoding="utf-8")


def test_azure_overlay_replaces_registry_and_opts_into_workload_identity() -> None:
    platform_values = (PLATFORM / "values-aks.yaml").read_text(encoding="utf-8")
    base_values = (PLATFORM / "values.yaml").read_text(encoding="utf-8")

    assert "replacewithacr.azurecr.io" in platform_values
    assert "ghcr.io" not in platform_values
    assert "pullSecrets: []" in platform_values
    assert 'azure.workload.identity/use: "true"' in platform_values
    # The portable delivery must stay provider-neutral.
    assert "podLabels: {}" in base_values
    assert "azure.workload.identity" not in base_values


def test_foundation_overlay_projects_the_runtime_identity_and_blob_provider() -> None:
    foundation_values = (FOUNDATION / "values-aks.yaml").read_text(encoding="utf-8")
    base_values = (FOUNDATION / "values.yaml").read_text(encoding="utf-8")

    assert "azure.workload.identity/client-id" in foundation_values
    assert "MEDIA_OBJECT_STORAGE_PROVIDER: azure" in foundation_values
    assert "MEDIA_AZURE_STORAGE_ACCOUNT_URL" in foundation_values
    # The account key belongs to the runtime Secret, never to a values file.
    assert "MEDIA_AZURE_STORAGE_ACCOUNT_KEY" not in foundation_values
    assert "annotations: {}" in base_values


def test_charts_forward_optional_pod_metadata() -> None:
    for template in ("api-workloads.yaml", "background-workloads.yaml"):
        text = (PLATFORM / "templates" / template).read_text(encoding="utf-8")
        assert "Values.podLabels" in text
        assert "Values.podAnnotations" in text
    account = (FOUNDATION / "templates" / "service-account.yaml").read_text(encoding="utf-8")
    assert "Values.serviceAccount.annotations" in account


def test_delivery_workflow_uses_oidc_and_cannot_apply() -> None:
    workflow = WORKFLOW.read_text(encoding="utf-8")

    assert "workflow_dispatch:" in workflow
    assert "id-token: write" in workflow
    assert "uses: azure/login@" in workflow
    assert "client-secret" not in workflow
    assert "secrets." not in workflow
    # Applying is deliberately absent until a remote state backend exists.
    assert "terraform apply" not in workflow
    assert "run_plan" in workflow
    assert "terraform" in workflow and "helm lint" in workflow
