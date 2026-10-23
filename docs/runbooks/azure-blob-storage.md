# Runbook — Azure Blob object storage

Operates the `AzureBlobObjectStorage` adapter from
[ADR-042](../adr/ADR-042-azure-blob-object-storage-adapter.md). MinIO remains the
default local provider; this runbook covers the `azure` provider and the
compatibility suite that proves it.

## Scope

- `media-service` object storage when `MEDIA_OBJECT_STORAGE_PROVIDER=azure`.
- Local compatibility verification against Azurite.
- Known behavioral differences between the S3 and Azure presigned-`PUT` contract.

Out of scope: provisioning a storage account, managed-identity signing, private
endpoints, and network policy. The repository does not provision Azure resources.

## Detection

| Symptom | Likely cause |
|---|---|
| `media-service` fails at startup with `object storage provider azure requires ...` | Provider set to `azure` but account URL, name, or key is empty |
| Uploads succeed but stored blobs report `application/octet-stream` | Direct uploader sent `Content-Type` without `x-ms-blob-content-type` |
| Direct upload returns `400` with an empty body | Direct uploader omitted `x-ms-blob-type: BlockBlob` |
| `AuthorizationFailure` or `403` on a presigned request | SAS expired, or the host clock is ahead of the SAS start time |
| `ContainerNotFound` | Container was never created; `ensure_bucket` runs at startup |
| Compose health check fails for `azurite` | Profile not enabled, or port 10000 is already bound |

## Impact

Media upload authorization, derivative generation, and asset deletion depend on
this adapter. Catalog and Chat attachment reads fail with `5xx` when the provider
is unreachable; nothing in PostgreSQL is lost, because object keys remain the
durable record.

## Immediate checks

```bash
# Which provider is the running process using?
docker compose -f infrastructure/compose/docker-compose.yml exec media-service \
  python -c "from media_service.config import get_settings; print(get_settings().object_storage_provider)"

# Is the compatibility endpoint reachable?
curl --silent --output /dev/null --write-out '%{http_code}\n' \
  "http://127.0.0.1:10000/devstoreaccount1?comp=list"   # 403 without a SAS is expected
```

## Safe mitigation

- Wrong provider or missing settings: set the variables explicitly and restart
  only `media-service`. Do not flip the provider on a running deployment without
  the compatibility suite passing first.
- Bad direct uploads: fix the uploader headers rather than relaxing validation.
  `head()` compares the stored content type with the authorized request.
- Provider outage: media writes and reads fail; the platform's object-storage
  failure path in the
  [media upload lifecycle](media-upload-lifecycle.md) governs the user-visible
  response. Do not delete rows or objects to clear the error.

## Verification: local compatibility suite

```bash
docker compose -f infrastructure/compose/docker-compose.yml --profile azure-blob up -d azurite

MEDIA_AZURE_STORAGE_LIVE=1 uv run --package media-service pytest \
  services/media-service/tests/test_azure_blob_storage.py -q
```

Without `MEDIA_AZURE_STORAGE_LIVE=1` the four selection tests still run and the
two live tests report `skipped` with a reason; a skipped live test is not
evidence that the adapter works.

The suite proves: container creation idempotency, `put_bytes`/`get_bytes`
round trip, metadata from `head`, presigned upload, presigned download with the
pinned `Content-Type`, and idempotent deletion of a missing key.

To point the suite somewhere else, set `MEDIA_AZURE_STORAGE_ACCOUNT_URL`,
`MEDIA_AZURE_STORAGE_ACCOUNT_NAME`, and `MEDIA_AZURE_STORAGE_ACCOUNT_KEY`.

## Switching `media-service` to Azurite end to end

```bash
export MEDIA_OBJECT_STORAGE_PROVIDER=azure
export MEDIA_AZURE_STORAGE_ACCOUNT_URL=http://azurite:10000/devstoreaccount1
export MEDIA_AZURE_STORAGE_ACCOUNT_NAME=devstoreaccount1
export MEDIA_AZURE_STORAGE_ACCOUNT_KEY='<azurite development key>'
export MEDIA_AZURE_STORAGE_PUBLIC_ENDPOINT_URL=http://localhost:10000/devstoreaccount1

docker compose -f infrastructure/compose/docker-compose.yml --profile azure-blob up -d
```

`MEDIA_AZURE_STORAGE_PUBLIC_ENDPOINT_URL` is what clients receive in presigned
URLs; the in-network `MEDIA_AZURE_STORAGE_ACCOUNT_URL` is never handed to a
browser. Keep both hosts aligned with how the edge reaches the service.

## Client contract difference

| Step | S3-compatible (current default) | Azure Blob |
|---|---|---|
| Upload | Presigned `PUT`; `Content-Type` header is the stored type | Presigned `PUT`; must send `x-ms-blob-type: BlockBlob` and `x-ms-blob-content-type` |
| Download | Presigned `GET` | Presigned `GET` with `Content-Type` pinned by the SAS (`rsct`) |
| Missing object on delete | No error | `ResourceNotFoundError` swallowed for idempotency |

## Recovery

1. Restore the provider the platform is expected to run (`s3`) or fix the Azure
   settings; restart `media-service` only.
2. Re-run the compatibility suite before declaring the provider healthy.
3. Re-drive failed cleanup or derivative tasks through the existing Celery task
   names; do not re-upload objects by hand.

## Escalation

Escalate when the compatibility suite fails against a real storage account but
passes against Azurite: that difference is provider behavior the adapter does not
model yet, and it needs an ADR-level decision rather than a code patch.

## Follow-up

- Extract both adapters into a shared package if a third service needs one.
- Add managed-identity or user-delegation SAS support when a deployment with
  Azure identity exists.
