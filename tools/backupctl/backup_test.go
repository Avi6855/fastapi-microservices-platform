package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

type memoryObject struct {
	data        []byte
	contentType string
}

type memoryStore struct {
	buckets map[string]map[string]memoryObject
}

func newMemoryStore() *memoryStore {
	return &memoryStore{buckets: map[string]map[string]memoryObject{}}
}

func (s *memoryStore) bucket(name string) map[string]memoryObject {
	if s.buckets[name] == nil {
		s.buckets[name] = map[string]memoryObject{}
	}
	return s.buckets[name]
}

func (s *memoryStore) EnsureBucket(_ context.Context, bucket string) error {
	s.bucket(bucket)
	return nil
}

func (s *memoryStore) List(_ context.Context, bucket, prefix string) ([]ObjectInfo, error) {
	objects := make([]ObjectInfo, 0, len(s.bucket(bucket)))
	for key, object := range s.bucket(bucket) {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		objects = append(objects, ObjectInfo{
			Key:         key,
			Size:        int64(len(object.data)),
			ContentType: object.contentType,
		})
	}
	// A real bucket returns keys in lexicographic order; mirror that so the
	// manifest is deterministic in tests too.
	for i := range objects {
		for j := i + 1; j < len(objects); j++ {
			if objects[j].Key < objects[i].Key {
				objects[i], objects[j] = objects[j], objects[i]
			}
		}
	}
	return objects, nil
}

func (s *memoryStore) Get(_ context.Context, bucket, key string) ([]byte, ObjectInfo, error) {
	object, ok := s.bucket(bucket)[key]
	if !ok {
		return nil, ObjectInfo{}, fmt.Errorf("object %q not found in %q", key, bucket)
	}
	return append([]byte(nil), object.data...), ObjectInfo{
		Key:         key,
		Size:        int64(len(object.data)),
		ContentType: object.contentType,
	}, nil
}

func (s *memoryStore) Put(_ context.Context, bucket, key, contentType string, data []byte) error {
	s.bucket(bucket)[key] = memoryObject{
		data:        append([]byte(nil), data...),
		contentType: contentType,
	}
	return nil
}

func (s *memoryStore) Delete(_ context.Context, bucket, key string) error {
	delete(s.bucket(bucket), key)
	return nil
}

func seededStore(t *testing.T) *memoryStore {
	t.Helper()
	store := newMemoryStore()
	for key, body := range map[string]string{
		"originals/a.webp": "first-object-bytes",
		"originals/b.webp": "second-object-bytes",
	} {
		if err := store.Put(context.Background(), "media", key, "image/webp", []byte(body)); err != nil {
			t.Fatalf("seed %q: %v", key, err)
		}
	}
	return store
}

func fixedTime() time.Time {
	return time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
}

func TestBackupWritesAVerifiedSnapshotWithAManifest(t *testing.T) {
	store := seededStore(t)

	result, err := Backup(context.Background(), store, BackupOptions{
		SourceBucket: "media",
		BackupBucket: "media-backups",
		SnapshotID:   "20261023T120000Z",
		Now:          fixedTime(),
	})
	if err != nil {
		t.Fatalf("backup: %v", err)
	}

	if result.ManifestKey != "snapshots/20261023T120000Z/manifest.json" {
		t.Fatalf("unexpected manifest key %q", result.ManifestKey)
	}
	if len(result.Manifest.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(result.Manifest.Entries))
	}
	if result.Manifest.TotalBytes != int64(len("first-object-bytes")+len("second-object-bytes")) {
		t.Fatalf("unexpected total bytes %d", result.Manifest.TotalBytes)
	}

	raw, _, err := store.Get(context.Background(), "media-backups", result.ManifestKey)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	manifest, err := DecodeManifest(raw)
	if err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	for _, entry := range manifest.Entries {
		if entry.ContentType != "image/webp" {
			t.Fatalf("entry %q lost its content type", entry.Key)
		}
		backupBytes, _, err := store.Get(
			context.Background(), "media-backups", manifest.ObjectKey(entry.Key),
		)
		if err != nil {
			t.Fatalf("read backup object %q: %v", entry.Key, err)
		}
		if checksum(backupBytes) != entry.SHA256 {
			t.Fatalf("backup object %q does not match its manifest entry", entry.Key)
		}
	}
}

func TestBackupEncodingIsDeterministic(t *testing.T) {
	store := seededStore(t)
	options := BackupOptions{
		SourceBucket: "media",
		BackupBucket: "media-backups",
		SnapshotID:   "20261023T120000Z",
		Now:          fixedTime(),
	}

	first, err := Backup(context.Background(), store, options)
	if err != nil {
		t.Fatalf("first backup: %v", err)
	}
	second, err := Backup(context.Background(), store, options)
	if err != nil {
		t.Fatalf("second backup: %v", err)
	}

	firstBytes, err := first.Manifest.Encode()
	if err != nil {
		t.Fatalf("encode first: %v", err)
	}
	secondBytes, err := second.Manifest.Encode()
	if err != nil {
		t.Fatalf("encode second: %v", err)
	}
	if string(firstBytes) != string(secondBytes) {
		t.Fatal("two identical snapshots encoded differently")
	}
}

func TestBackupRequiresBothBuckets(t *testing.T) {
	if _, err := Backup(context.Background(), newMemoryStore(), BackupOptions{}); err == nil {
		t.Fatal("expected an error when buckets are missing")
	}
}

func TestVerifyDetectsCorruptionAndReportsEveryFailure(t *testing.T) {
	store := seededStore(t)
	result, err := Backup(context.Background(), store, BackupOptions{
		SourceBucket: "media",
		BackupBucket: "media-backups",
		SnapshotID:   "20261023T120000Z",
		Now:          fixedTime(),
	})
	if err != nil {
		t.Fatalf("backup: %v", err)
	}

	corruptKey := result.Manifest.ObjectKey("originals/a.webp")
	if err := store.Put(context.Background(), "media-backups", corruptKey, "image/webp", []byte("tampered")); err != nil {
		t.Fatalf("corrupt object: %v", err)
	}

	verified, err := Verify(context.Background(), store, "media-backups", "20261023T120000Z")
	if err == nil {
		t.Fatal("expected verification to fail on a corrupt snapshot")
	}
	if len(verified.Failures) != 1 {
		t.Fatalf("expected 1 failure, got %d: %v", len(verified.Failures), verified.Failures)
	}
	if !strings.Contains(err.Error(), "originals/a.webp") {
		t.Fatalf("failure does not name the object: %v", err)
	}
}

func TestRestoreRebuildsADeletedBucket(t *testing.T) {
	store := seededStore(t)
	if _, err := Backup(context.Background(), store, BackupOptions{
		SourceBucket: "media",
		BackupBucket: "media-backups",
		SnapshotID:   "20261023T120000Z",
		Now:          fixedTime(),
	}); err != nil {
		t.Fatalf("backup: %v", err)
	}

	delete(store.buckets, "media")

	result, err := Restore(context.Background(), store, RestoreOptions{
		BackupBucket: "media-backups",
		SnapshotID:   "20261023T120000Z",
		TargetBucket: "media",
	})
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if result.Restored != 2 {
		t.Fatalf("expected 2 restored objects, got %d", result.Restored)
	}

	restored, _, err := store.Get(context.Background(), "media", "originals/b.webp")
	if err != nil {
		t.Fatalf("read restored object: %v", err)
	}
	if string(restored) != "second-object-bytes" {
		t.Fatalf("restored bytes differ: %q", restored)
	}

	if _, err := Verify(context.Background(), store, "media-backups", "20261023T120000Z"); err != nil {
		t.Fatalf("snapshot no longer verifies after restore: %v", err)
	}
}

func TestRestoreDryRunWritesNothing(t *testing.T) {
	store := seededStore(t)
	if _, err := Backup(context.Background(), store, BackupOptions{
		SourceBucket: "media",
		BackupBucket: "media-backups",
		SnapshotID:   "20261023T120000Z",
		Now:          fixedTime(),
	}); err != nil {
		t.Fatalf("backup: %v", err)
	}

	result, err := Restore(context.Background(), store, RestoreOptions{
		BackupBucket: "media-backups",
		SnapshotID:   "20261023T120000Z",
		TargetBucket: "media-restored",
		DryRun:       true,
	})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if !result.DryRun || result.Restored != 2 {
		t.Fatalf("unexpected dry-run result %+v", result)
	}
	if len(store.buckets["media-restored"]) != 0 {
		t.Fatal("dry run wrote objects into the target bucket")
	}
}

func TestRestoreRefusesACorruptBackup(t *testing.T) {
	store := seededStore(t)
	result, err := Backup(context.Background(), store, BackupOptions{
		SourceBucket: "media",
		BackupBucket: "media-backups",
		SnapshotID:   "20261023T120000Z",
		Now:          fixedTime(),
	})
	if err != nil {
		t.Fatalf("backup: %v", err)
	}

	if err := store.Put(
		context.Background(),
		"media-backups",
		result.Manifest.ObjectKey("originals/a.webp"),
		"image/webp",
		[]byte("tampered"),
	); err != nil {
		t.Fatalf("corrupt object: %v", err)
	}

	if _, err := Restore(context.Background(), store, RestoreOptions{
		BackupBucket: "media-backups",
		SnapshotID:   "20261023T120000Z",
		TargetBucket: "media-restored",
	}); err == nil {
		t.Fatal("expected restore to refuse a corrupt snapshot")
	}
	if len(store.buckets["media-restored"]) != 0 {
		t.Fatal("restore wrote objects from a corrupt snapshot")
	}
}

func TestRestoreRequiresAReadableManifest(t *testing.T) {
	_, err := Restore(context.Background(), newMemoryStore(), RestoreOptions{
		BackupBucket: "media-backups",
		SnapshotID:   "missing",
		TargetBucket: "media",
	})
	if err == nil {
		t.Fatal("expected an error for a missing manifest")
	}
	if !strings.Contains(err.Error(), "manifest") {
		t.Fatalf("error does not mention the manifest: %v", err)
	}
}

func TestDecodeManifestRejectsUnsupportedOrIncompleteRecords(t *testing.T) {
	cases := map[string]string{
		"wrong version": `{"version":2,"snapshot_id":"a","source_bucket":"s","backup_bucket":"b"}`,
		"no snapshot":   `{"version":1,"source_bucket":"s","backup_bucket":"b"}`,
		"no source":     `{"version":1,"snapshot_id":"a","backup_bucket":"b"}`,
		"bad checksum": `{"version":1,"snapshot_id":"a","source_bucket":"s","backup_bucket":"b",
			"entries":[{"key":"k","size":1,"content_type":"t","sha256":"short"}]}`,
	}

	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeManifest([]byte(payload)); err == nil {
				t.Fatal("expected the manifest to be rejected")
			}
		})
	}
}

func TestVerifyFailsOnAMissingSnapshot(t *testing.T) {
	_, err := Verify(context.Background(), newMemoryStore(), "media-backups", "20261023T120000Z")
	if err == nil {
		t.Fatal("expected an error for a missing snapshot")
	}
	if !strings.Contains(err.Error(), "manifest") {
		t.Fatalf("unexpected error: %v", err)
	}
	if errors.Is(err, context.Canceled) {
		t.Fatal("unexpected context error")
	}
}
