package main

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// BackupOptions describes one snapshot run.
type BackupOptions struct {
	SourceBucket string
	BackupBucket string
	Prefix       string
	SnapshotID   string
	Now          time.Time
}

// BackupResult summarizes a completed snapshot.
type BackupResult struct {
	Manifest    Manifest
	ManifestKey string
}

// Backup copies every object under the prefix into a new snapshot and writes the
// manifest last. Each copy is read back and checksummed before it is recorded: a
// backup that cannot prove its own bytes is worse than no backup, because it
// passes as one.
func Backup(ctx context.Context, store Store, options BackupOptions) (BackupResult, error) {
	if options.SourceBucket == "" || options.BackupBucket == "" {
		return BackupResult{}, fmt.Errorf("source and backup buckets are required")
	}
	now := options.Now
	if now.IsZero() {
		now = time.Now()
	}
	snapshotID := options.SnapshotID
	if snapshotID == "" {
		snapshotID = newSnapshotID(now)
	}

	manifest := Manifest{
		Version:      manifestVersion,
		CreatedAt:    now.UTC(),
		SourceBucket: options.SourceBucket,
		BackupBucket: options.BackupBucket,
		SnapshotID:   snapshotID,
	}

	if err := store.EnsureBucket(ctx, options.BackupBucket); err != nil {
		return BackupResult{}, fmt.Errorf("ensure backup bucket %q: %w", options.BackupBucket, err)
	}
	objects, err := store.List(ctx, options.SourceBucket, options.Prefix)
	if err != nil {
		return BackupResult{}, fmt.Errorf("list %q: %w", options.SourceBucket, err)
	}

	for _, object := range objects {
		data, info, err := store.Get(ctx, options.SourceBucket, object.Key)
		if err != nil {
			return BackupResult{}, fmt.Errorf("read source object %q: %w", object.Key, err)
		}
		contentType := info.ContentType
		if contentType == "" {
			contentType = object.ContentType
		}
		sum := checksum(data)
		backupKey := manifest.ObjectKey(object.Key)
		if err := store.Put(ctx, options.BackupBucket, backupKey, contentType, data); err != nil {
			return BackupResult{}, fmt.Errorf("write backup object %q: %w", backupKey, err)
		}

		restoredBytes, _, err := store.Get(ctx, options.BackupBucket, backupKey)
		if err != nil {
			return BackupResult{}, fmt.Errorf("read back backup object %q: %w", backupKey, err)
		}
		if checksum(restoredBytes) != sum {
			return BackupResult{}, fmt.Errorf("backup object %q does not match its source", backupKey)
		}

		manifest.Entries = append(manifest.Entries, Entry{
			Key:         object.Key,
			Size:        int64(len(data)),
			ContentType: contentType,
			SHA256:      sum,
		})
		manifest.TotalBytes += int64(len(data))
	}

	encoded, err := manifest.Encode()
	if err != nil {
		return BackupResult{}, err
	}
	if err := store.Put(ctx, options.BackupBucket, manifest.ManifestKey(), "application/json", encoded); err != nil {
		return BackupResult{}, fmt.Errorf("write manifest: %w", err)
	}
	return BackupResult{Manifest: manifest, ManifestKey: manifest.ManifestKey()}, nil
}

// VerifyResult reports what a verification pass checked and what it found.
type VerifyResult struct {
	SnapshotID string
	Entries    int
	TotalBytes int64
	Failures   []string
}

// Verify re-reads every object of a snapshot and compares size and checksum with
// the manifest. It reports every failure instead of stopping at the first one, so
// one run describes the whole damage.
func Verify(ctx context.Context, store Store, backupBucket, snapshotID string) (VerifyResult, error) {
	manifest, err := loadManifest(ctx, store, backupBucket, snapshotID)
	if err != nil {
		return VerifyResult{}, err
	}

	result := VerifyResult{SnapshotID: manifest.SnapshotID, Entries: len(manifest.Entries)}
	for _, entry := range manifest.Entries {
		data, _, err := store.Get(ctx, backupBucket, manifest.ObjectKey(entry.Key))
		if err != nil {
			result.Failures = append(result.Failures, fmt.Sprintf("%s: unreadable: %v", entry.Key, err))
			continue
		}
		switch {
		case int64(len(data)) != entry.Size:
			result.Failures = append(
				result.Failures,
				fmt.Sprintf("%s: size %d, expected %d", entry.Key, len(data), entry.Size),
			)
		case checksum(data) != entry.SHA256:
			result.Failures = append(result.Failures, fmt.Sprintf("%s: checksum mismatch", entry.Key))
		default:
			result.TotalBytes += entry.Size
		}
	}
	if len(result.Failures) > 0 {
		return result, fmt.Errorf(
			"snapshot %s failed verification: %s",
			manifest.SnapshotID,
			strings.Join(result.Failures, "; "),
		)
	}
	return result, nil
}

// RestoreOptions describes one restore run.
type RestoreOptions struct {
	BackupBucket string
	SnapshotID   string
	TargetBucket string
	DryRun       bool
}

// RestoreResult summarizes a restore run.
type RestoreResult struct {
	SnapshotID string
	Restored   int
	TotalBytes int64
	DryRun     bool
}

// Restore writes a snapshot back into a bucket and verifies every object on the
// way in and on the way out. A dry run performs the same reads and checks but
// writes nothing, which is what a restore drill should do first.
func Restore(ctx context.Context, store Store, options RestoreOptions) (RestoreResult, error) {
	if options.BackupBucket == "" || options.TargetBucket == "" {
		return RestoreResult{}, fmt.Errorf("backup and target buckets are required")
	}
	manifest, err := loadManifest(ctx, store, options.BackupBucket, options.SnapshotID)
	if err != nil {
		return RestoreResult{}, err
	}

	result := RestoreResult{SnapshotID: manifest.SnapshotID, DryRun: options.DryRun}
	if !options.DryRun {
		if err := store.EnsureBucket(ctx, options.TargetBucket); err != nil {
			return result, fmt.Errorf("ensure target bucket %q: %w", options.TargetBucket, err)
		}
	}

	for _, entry := range manifest.Entries {
		data, _, err := store.Get(ctx, options.BackupBucket, manifest.ObjectKey(entry.Key))
		if err != nil {
			return result, fmt.Errorf("read backup object %q: %w", entry.Key, err)
		}
		if checksum(data) != entry.SHA256 {
			return result, fmt.Errorf("backup object %q is corrupt; refusing to restore it", entry.Key)
		}
		if options.DryRun {
			result.Restored++
			result.TotalBytes += entry.Size
			continue
		}
		if err := store.Put(ctx, options.TargetBucket, entry.Key, entry.ContentType, data); err != nil {
			return result, fmt.Errorf("write restored object %q: %w", entry.Key, err)
		}

		restoredBytes, _, err := store.Get(ctx, options.TargetBucket, entry.Key)
		if err != nil {
			return result, fmt.Errorf("read back restored object %q: %w", entry.Key, err)
		}
		if checksum(restoredBytes) != entry.SHA256 {
			return result, fmt.Errorf("restored object %q does not match the manifest", entry.Key)
		}
		result.Restored++
		result.TotalBytes += entry.Size
	}
	return result, nil
}

func loadManifest(ctx context.Context, store Store, backupBucket, snapshotID string) (Manifest, error) {
	if snapshotID == "" {
		return Manifest{}, fmt.Errorf("snapshot id is required")
	}
	key := "snapshots/" + snapshotID + "/" + manifestName
	data, _, err := store.Get(ctx, backupBucket, key)
	if err != nil {
		return Manifest{}, fmt.Errorf("read manifest %q: %w", key, err)
	}
	return DecodeManifest(data)
}
