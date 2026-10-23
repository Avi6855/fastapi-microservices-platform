# ADR-044 — Verified object-storage backup and restore drill

- Status: `Accepted`
- Date: `2026-10-23`
- Owners: platform architecture
- Supersedes: none
- Superseded by: none

## Context

[ADR-008](ADR-008-s3-compatible-object-storage.md) put media objects behind an
`ObjectStorage` port, and [ADR-042](ADR-042-azure-blob-object-storage-adapter.md)
proved a second provider behind it. Both decisions optimize how the platform
writes and reads objects. Neither answers the question a data-protection team asks
first: if the bucket is gone, what exactly comes back, and how would anyone know
it came back intact?

PostgreSQL has migrations and tests; object storage had no restore path at all. A
backup that is never restored is a belief, not a control, so the missing piece is
a drill that proves byte equality.

## Decision

Add `tools/backupctl`, a Go command that backs up, verifies, and restores an
S3-compatible bucket using a written, versioned manifest:

- **Snapshot on write.** `backup` copies every object under an optional prefix into
  `snapshots/<id>/objects/<key>`, then **reads each copy back** and compares
  SHA-256 before recording it. The manifest is written last and contains the
  format version, UTC creation time, both buckets, and one entry per object with
  key, size, content type, and checksum.
- **Verify independently.** `verify` re-reads a snapshot and reports every
  mismatch instead of stopping at the first, so one run describes the damage.
- **Restore with proof.** `restore` checks the backup bytes before writing, writes
  only when the checksum matches, then reads the object back from the target and
  compares again. `--dry-run` performs every read and check and writes nothing.
- **Port before provider.** All logic depends on a `Store` interface; `MinIOStore`
  is the only S3-aware file. The unit suite runs against an in-memory store, so the
  semantics are tested without a server.
- **Small, hardened image.** A static binary in a `scratch` runtime with CA roots,
  running as uid `10001`, with no shell and no package manager.
- **CI gate.** A dedicated workflow job installs a pinned Go toolchain by checksum,
  then runs `gofmt`, `go vet`, `go test`, and `go build`.

Manifest entries are sorted by key and timestamps are normalized to UTC, so two
identical snapshots encode identically and a diff of two manifests is meaningful.

## Consequences

### Positive

- "We can restore media" becomes a command with an exit status, a checksum, and a
  reproducible procedure rather than a claim.
- The drill runs anywhere an S3-compatible endpoint exists, including the local
  MinIO the platform already ships.
- Corruption fails loudly and names the object, which is what an incident review
  needs.
- The tool is independent of the Python services, so a restore does not depend on
  the platform's own health.

### Negative and risks

- Objects are buffered in memory to hash them. That matches the platform's bounded
  media limit but is not a tool for multi-gigabyte objects.
- Snapshots double storage for the backed-up bucket until retention removes them.
  Retention policy is documented, not implemented.
- The manifest is the only index. Losing the manifest loses the ability to verify
  that snapshot, which is why it is written last and named explicitly.
- Go is now a second language in the repository, with its own toolchain pin and CI
  job.

## Compatibility and migration

Nothing in the platform changes: no contract, schema, or service behavior. The
tool is additive and reads only the object namespace the services already use.

Migration and rollback are therefore about the drill, not the platform:

1. Take a snapshot before any storage migration. A provider cutover should be
   preceded and followed by a snapshot and a `verify`.
2. Restore into a **new** bucket first and compare; overwriting the live bucket is
   a deliberate, separate step.
3. Rolling back the tool means deleting snapshots; rolling back a cutover means
   restoring from the snapshot taken before it.

The manifest carries `version`, and an unknown version is rejected rather than
guessed at, so a future format change fails loudly instead of restoring wrong
bytes.

## Boundaries

- **S3-compatible endpoints only.** The tool speaks the S3 API; it cannot back up
  Azure Blob, even though `media-service` can store there through ADR-042. A drill
  for that provider needs its own adapter and is not claimed here.
- **Media bucket scope.** `order-service` invoice objects still use their own S3
  adapter; the tool is not wired into either service's runtime.
- **No scheduler, no retention executor, no off-site replication.** A deployment
  would schedule the command and store manifests where they can be audited.
- **Verification is not security.** Checksums prove integrity, not authorization;
  snapshot buckets need the same access control as the source.
- No managed backup service (Azure Backup, AWS Backup, Velero) is configured. The
  repository has no subscription to back such a claim, and the drill must be
  reproducible on a workstation.

## Alternatives considered

- Managed cloud backup (Azure Backup, AWS Backup) or Velero: rejected for this
  repository because none of it can be demonstrated without an owned environment,
  and an unverifiable configuration would repeat the claim problem this ADR exists
  to avoid. The Azure target in ADR-043 is where such a service would attach later.
- `rclone` or `mc mirror` in a script: rejected because mirroring copies bytes but
  produces no manifest, so a restore cannot prove which snapshot it came from or
  whether the copy was complete.
- Implementing the drill in Python next to `media-service`: rejected because the
  operator path should not require the platform's dependency graph or its health,
  and because a small Go command keeps the drill independent of the code it backs
  up.

## Validation

Executed while authoring this decision:

```bash
cd tools/backupctl
gofmt -l .
go vet ./...
go test ./... -count=1
go build -trimpath -o /tmp/backupctl .
```

The unit suite covers: verified snapshot contents, deterministic manifest
encoding, corruption detection naming the object, restore into a deleted bucket,
dry-run writing nothing, refusal to restore a corrupt snapshot, manifest version
and field validation, and missing-manifest errors.

An end-to-end drill ran against the repository's pinned MinIO image on a spare
port: seed three objects, `backup` (3 objects, 351 bytes), `verify`, delete every
source object, `restore` into a new bucket, then compare every restored object's
SHA-256 and content type with the originals. All matched. Corrupting one backup
object then made `verify` fail with `size 18, expected 120` and `restore` refuse
with `corrupt; refusing to restore it`, both exiting non-zero.

The container image builds from the pinned Go base and runs as uid `10001`; its
command prints usage. The image is **not** built or published by CI, and the tool
has never been run against a cloud endpoint.

## Related material

- [Object-storage backup and restore runbook](../runbooks/object-storage-backup-restore.md)
- [ADR-008 — S3-compatible object storage](ADR-008-s3-compatible-object-storage.md)
- [ADR-042 — Azure Blob adapter](ADR-042-azure-blob-object-storage-adapter.md)
- [Media upload lifecycle runbook](../runbooks/media-upload-lifecycle.md)
- [Tool README](../../tools/backupctl/README.md)
