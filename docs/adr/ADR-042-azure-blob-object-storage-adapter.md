# ADR-042 — Azure Blob Storage adapter behind the object-storage port

- Status: `Accepted`
- Date: `2026-10-23`
- Owners: platform architecture
- Supersedes: none
- Superseded by: none

## Context

[ADR-008](ADR-008-s3-compatible-object-storage.md) put binary objects behind an
`ObjectStorage` port and shipped one adapter, `S3ObjectStorage`, backed by
MinIO locally. That decision claims provider portability, but until a second
adapter exists that claim is untested: presigned transfer, content-type
handling, idempotent deletion, and error surfaces are all S3-shaped.

Azure Blob Storage is the storage provider of a plausible deployment target
for this platform. Adding it also answers the review question a port is
supposed to answer: what actually changes when the provider changes?

## Decision

Add `AzureBlobObjectStorage` to `media-service` behind the same port, and select
the adapter with configuration:

- `MEDIA_OBJECT_STORAGE_PROVIDER` — `s3` (default) or `azure`.
- `MEDIA_AZURE_STORAGE_ACCOUNT_URL`, `MEDIA_AZURE_STORAGE_ACCOUNT_NAME`,
  `MEDIA_AZURE_STORAGE_ACCOUNT_KEY`, `MEDIA_AZURE_STORAGE_CONTAINER`, and an
  optional `MEDIA_AZURE_STORAGE_PUBLIC_ENDPOINT_URL` for the origin clients use.

Mapping and behavior:

- A Blob container plays the role of a bucket; a blob plays the role of an object.
  `ensure_bucket`, `put_bytes`, `get_bytes`, `head`, and `delete_objects` keep
  their existing semantics, including idempotent deletion of missing keys.
- Direct client transfer uses a short-lived service SAS signed with the account
  key. Account-key signing is the only form Azurite and a plain storage account
  share, so it is the only form that can be proven here.
- The upload SAS pins the download `Content-Type` (`rsct`). Because Azure stores
  blob metadata from `x-ms-blob-content-type` and requires `x-ms-blob-type` on
  `PUT`, a direct uploader must send those two headers when the provider is
  Azure; the [runbook](../runbooks/azure-blob-storage.md) records the difference
  next to the S3 presigned-`PUT` contract that needs neither.
- Background retry policy consumes `STORAGE_TRANSIENT_ERRORS`, a provider-neutral
  tuple covering `BotoCoreError`, `AzureError`, and `ConnectionError`, so Celery
  tasks no longer hard-code the S3 library.
- `infrastructure/compose/docker-compose.yml` gains an `azurite` service under the
  opt-in `azure-blob` profile. MinIO stays the default local provider, and the
  default topology is unchanged.

The compatibility suite in
[`test_azure_blob_storage.py`](../../services/media-service/tests/test_azure_blob_storage.py)
runs a full round trip, presigned upload, and presigned download against a
reachable Blob endpoint. It is opt-in, like the Compose E2E suite, because the
default local topology does not include one.

## Consequences

### Positive

- Provider portability is demonstrated by a second adapter with its own tests
  rather than asserted in prose.
- Business logic in `media-service` keeps depending only on the port.
- The local verification path needs no Azure subscription: Azurite is the
  compatibility endpoint, and the suite is reproducible on a workstation.

### Negative and risks

- Two adapters now exist for one port; a provider-neutral behavior contract has
  to be maintained by tests instead of being implied by a single implementation.
- Account-key SAS management means a storage key is a runtime secret, rotated
  outside the repository.
- The adapters are still synchronous, like the existing S3 adapter; they are
  called through `asyncio.to_thread`.

## Compatibility and migration

The port is unchanged, so no service, contract, or schema changes with the
provider. Migration is configuration plus a cutover check:

1. Create the destination container and grant the runtime identity the access it
   needs; `ensure_bucket` runs at startup and is idempotent.
2. Run the compatibility suite against the destination endpoint. A suite that
   only skips its live tests is not a cutover check.
3. Switch `MEDIA_OBJECT_STORAGE_PROVIDER` and restart `media-service` only.
4. Existing objects are not copied by this change. A real cutover either copies
   and checksum-verifies objects or serves them through a deliberate
   dual-provider window; the metadata rows keep pointing at the same keys.
5. Rolling back is the same switch in reverse. MinIO objects stay intact, so the
   rollback path does not depend on an Azure export.

Presigned-URL clients must apply the Azure header contract described above when
flipping providers; the runbook lists the exact difference so a client does not
silently store `application/octet-stream`.

## Boundaries

- No Azure subscription, resource group, or deployment is created or claimed by
  this repository. Nothing here has been run against a real storage account.
- Managed identity, user-delegation SAS, private endpoints, immutability policy,
  and lifecycle management are not implemented. They need an owned deployment.
- `order-service` invoice storage still uses its own S3 adapter. Extracting both
  adapters into a shared package is a separate decision and is not done here.
- Azurite-specific paths (`--skipApiVersionCheck`, the well-known development
  account) never reach business logic; they exist only in the opt-in profile
  and the test defaults.

## Alternatives considered

- Keep S3 adapters only and treat Azure as a future concern: rejected, because
  the portability claim then stays unverified and the media flow keeps an
  untested provider assumption.
- Replace the adapter in both services at once with a shared library: rejected
  for now because it would be a cross-service extraction bundled with a provider
  change, against this repository's one-decision-per-ADR rule.
- Use the Azure SDK async clients: rejected, because the port is synchronous and
  the existing call sites already offload to `asyncio.to_thread`; an async
  adapter would change the port.

## Validation

```bash
# Default provider is unchanged.
uv run --package media-service pytest services/media-service/tests -q

# Provider-compatibility suite against Azurite on port 10000.
docker compose -f infrastructure/compose/docker-compose.yml --profile azure-blob up -d azurite
MEDIA_AZURE_STORAGE_LIVE=1 uv run --package media-service pytest \
  services/media-service/tests/test_azure_blob_storage.py -q
```

## Related material

- [ADR-008 — S3-compatible object storage](ADR-008-s3-compatible-object-storage.md)
- [Azure Blob object-storage runbook](../runbooks/azure-blob-storage.md)
- [Media and invoice flows](../diagrams/media-and-invoice.md)
- [Media upload lifecycle runbook](../runbooks/media-upload-lifecycle.md)
