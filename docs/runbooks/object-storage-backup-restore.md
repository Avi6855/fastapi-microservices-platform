# Runbook — object-storage backup and restore drill

Operates `tools/backupctl` from
[ADR-044](../adr/ADR-044-verified-object-storage-backup-drill.md). The tool backs
up an S3-compatible bucket into a snapshot with a checksummed manifest, verifies a
snapshot independently, and restores it with per-object proof.

## Scope

- `media-service` media objects in S3-compatible storage (MinIO locally).
- Any S3-compatible endpoint the operator can reach, including a cloud bucket.
- Out of scope: Azure Blob snapshots, invoice objects from `order-service`,
  scheduling, retention execution, and cross-region replication.

## Detection

| Symptom | Likely cause |
|---|---|
| `verify` reports `size N, expected M` | The stored copy was truncated or overwritten |
| `verify` reports `checksum mismatch` | Bytes changed after the snapshot was written |
| `verify` reports `unreadable` | The object was deleted or the credential lost access |
| `restore` fails with `refusing to restore it` | The snapshot is corrupt; the drill is doing its job |
| `backup` fails with `does not match its source` | The target endpoint mutated or truncated the copy |
| `backup` fails with `endpoint is required` | Connection flags or `BACKUPCTL_*` variables are missing |

## Impact

A failed `verify` means the snapshot cannot be trusted for recovery. The live
bucket is unaffected by verification. A failed restore leaves the target bucket
with only the objects that already passed checking; it never writes an object
whose checksum did not match.

## Immediate checks

```bash
# Is the endpoint reachable at all?
curl --silent --output /dev/null --write-out '%{http_code}\n' http://127.0.0.1:9100/minio/health/live

# What does the snapshot claim, and does it still verify?
BACKUPCTL_ENDPOINT=127.0.0.1:9100 BACKUPCTL_ACCESS_KEY=... BACKUPCTL_SECRET_KEY=... \
  backupctl verify --backup media-backups --snapshot <snapshot-id>
```

## Safe mitigation

- One corrupt object: take a fresh snapshot from the live bucket if it is healthy,
  and keep the damaged snapshot for inspection. Do not patch objects inside a
  snapshot; that destroys the evidence trail.
- Live bucket damaged: restore the last verifying snapshot into a **new** bucket,
  compare, then decide about the production cutover.
- Lost manifest: the snapshot is unverifiable and must be treated as invalid, even
  if its objects look complete.
- Never point `--target` at the live bucket during an investigation.

## The drill (local, reproducible)

```bash
# 1. Start the pinned MinIO image on a spare port.
docker run -d --name drill-minio -p 127.0.0.1:9100:9000 \
  -e MINIO_ROOT_USER=backupctl -e MINIO_ROOT_PASSWORD=backupctl-local-only \
  -v drill-minio-data:/data \
  fastapi-microservices-platform-minio:RELEASE.2025-10-15T17-29-55Z-local server /data

# 2. Build the tool.
cd tools/backupctl && go build -trimpath -o /tmp/backupctl . && cd ../..

# 3. Seed a bucket, then take and verify a snapshot.
#    (Any S3 client works; the runbook assumes the platform's boto3 environment.)
export BACKUPCTL_ENDPOINT=127.0.0.1:9100
export BACKUPCTL_ACCESS_KEY=backupctl
export BACKUPCTL_SECRET_KEY=backupctl-local-only

/tmp/backupctl backup  --source media-drill --backup media-backups --snapshot drill-1
/tmp/backupctl verify  --backup media-backups --snapshot drill-1

# 4. Destroy the source, prove the restore.
/tmp/backupctl restore --backup media-backups --snapshot drill-1 --target media-restored --dry-run
/tmp/backupctl restore --backup media-backups --snapshot drill-1 --target media-restored
```

Step 4 is the point of the whole procedure: deleting the source is what turns a
copy into a restore. Compare restored SHA-256 values and content types with the
originals before reporting a successful drill.

## Reading a failure

- `size N, expected M`: the copy is truncated. Recopy from the live bucket if it is
  healthy; otherwise this snapshot is lost for that object.
- `checksum mismatch`: the bytes changed. Treat the storage credential as
  suspect and check who can write to the snapshot bucket.
- `unreadable`: the object or the bucket is gone, or the credential lost read
  access. Check bucket policy before assuming data loss.

## Recovery

1. Restore into a new bucket and verify it.
2. Compare object counts, total bytes, and content types with the manifest.
3. Only then switch the service's bucket configuration, and only with the
   platform's normal change control.
4. Re-run `verify` on the source snapshot afterwards, so the evidence is recorded
   after the recovery, not just before it.

## Escalation

Escalate when a snapshot verifies but the restored objects cannot be served by
`media-service`: that is a content-type, key, or metadata gap in the drill, and it
belongs in an ADR change rather than an ad-hoc patch.

## Follow-up

- Schedule snapshots and implement retention with a documented window.
- Add an Azure Blob adapter for the drill if the platform runs on ADR-043's target.
- Store manifests outside the backup bucket so a bucket loss cannot take the index
  with it.
