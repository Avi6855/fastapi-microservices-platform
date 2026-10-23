# backupctl

An object-storage backup, verification, and restore drill for the platform's media
bucket. Written in Go because the drill has to run outside the Python services and
against any S3-compatible endpoint.

The design rule is simple: a backup is not evidence until it has been read back
and checksummed, and a restore is not done until the restored bytes match the
manifest. Every command proves its own work.

## Commands

```bash
# Take a snapshot of one bucket into another, then verify each copy.
backupctl backup --source media --backup media-backups --snapshot 20261023T120000Z

# Re-read a snapshot and compare every object with the manifest.
backupctl verify --backup media-backups --snapshot 20261023T120000Z

# Restore into a bucket. Always dry-run first: it reads and checks everything and
# writes nothing.
backupctl restore --backup media-backups --snapshot 20261023T120000Z \
  --target media-restored --dry-run
backupctl restore --backup media-backups --snapshot 20261023T120000Z --target media-restored
```

Connection settings come from flags or environment variables:

| Flag | Environment | Meaning |
|---|---|---|
| `--endpoint` | `BACKUPCTL_ENDPOINT` | S3-compatible host and port |
| `--access-key` | `BACKUPCTL_ACCESS_KEY` | access key |
| `--secret-key` | `BACKUPCTL_SECRET_KEY` | secret key |
| `--tls` | `BACKUPCTL_SECURE=true` | use HTTPS |

Exit status is `0` on success and `1` when a check fails, so the drill can run in
CI without parsing output.

## Snapshot layout

```text
snapshots/<snapshot-id>/
  manifest.json                 # written last; without it the snapshot is invalid
  objects/<original-key>        # byte-identical copy, checksum recorded
```

The manifest records `version`, the UTC creation time, both bucket names, and one
entry per object with key, size, content type, and SHA-256. Entries are sorted and
timestamps normalized, so two identical snapshots encode identically and a diff
between manifests is meaningful.

## Guarantees and limits

- **Verified copy.** `backup` reads each object back from the backup bucket before
  writing the manifest entry. A copy that cannot be read back fails the snapshot
  instead of passing as one.
- **Verified restore.** `restore` checks the backup bytes before writing, then reads
  the object back from the target and compares the checksum again.
- **Corruption is refused, not repaired.** A mismatch fails the command with the
  object key. Nothing is silently skipped.
- **Whole object in memory.** Each object is buffered so it can be hashed. This
  matches the platform's bounded media objects; a tool for multi-gigabyte objects
  would stream and hash instead.
- **Not a scheduler.** Retention, scheduling, and cross-region replication are not
  here. The runbook covers the drill; a real deployment would schedule it and
  store the manifests where the platform can audit them.

## Local drill

```bash
# MinIO from the repository's pinned image, on a spare port.
docker run -d --name drill-minio -p 127.0.0.1:9100:9000 \
  -e MINIO_ROOT_USER=backupctl -e MINIO_ROOT_PASSWORD=backupctl-local-only \
  fastapi-microservices-platform-minio:RELEASE.2025-10-15T17-29-55Z-local server /data

go build -o /tmp/backupctl .
BACKUPCTL_ENDPOINT=127.0.0.1:9100 BACKUPCTL_ACCESS_KEY=backupctl \
  BACKUPCTL_SECRET_KEY=backupctl-local-only /tmp/backupctl backup \
  --source media-drill --backup media-backups --snapshot drill-1
```

The full procedure, including the destructive step and how to read a failure, is
in [`docs/runbooks/object-storage-backup-restore.md`](../../docs/runbooks/object-storage-backup-restore.md).
