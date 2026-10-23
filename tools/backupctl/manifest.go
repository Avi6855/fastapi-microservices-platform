package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"
)

const (
	manifestVersion = 1
	manifestName    = "manifest.json"
	objectsPrefix   = "objects/"
)

// Entry is one object in a snapshot. SHA256 is the checksum of the stored bytes,
// not of the provider metadata, so a restore can prove byte equality.
type Entry struct {
	Key         string `json:"key"`
	Size        int64  `json:"size"`
	ContentType string `json:"content_type"`
	SHA256      string `json:"sha256"`
}

// Manifest is the durable record of a snapshot. It is written last, so a
// snapshot without a manifest is an incomplete backup rather than a valid one.
type Manifest struct {
	Version      int       `json:"version"`
	CreatedAt    time.Time `json:"created_at"`
	SourceBucket string    `json:"source_bucket"`
	BackupBucket string    `json:"backup_bucket"`
	SnapshotID   string    `json:"snapshot_id"`
	Entries      []Entry   `json:"entries"`
	TotalBytes   int64     `json:"total_bytes"`
}

// SnapshotPrefix is the key prefix that holds one snapshot: its objects and its
// manifest, so a snapshot can be copied or deleted as a unit.
func (m Manifest) SnapshotPrefix() string {
	return "snapshots/" + m.SnapshotID + "/"
}

// ManifestKey is the key of the manifest inside the backup bucket.
func (m Manifest) ManifestKey() string {
	return m.SnapshotPrefix() + manifestName
}

// ObjectKey is the backup key for a source object key.
func (m Manifest) ObjectKey(key string) string {
	return m.SnapshotPrefix() + objectsPrefix + key
}

// Encode returns the deterministic JSON form of the manifest. Entries are sorted
// by key and timestamps are UTC, so two identical snapshots encode identically
// and a diff of two manifests is meaningful.
func (m Manifest) Encode() ([]byte, error) {
	normalized := m
	normalized.CreatedAt = m.CreatedAt.UTC()
	normalized.Entries = append([]Entry(nil), m.Entries...)
	sort.Slice(normalized.Entries, func(i, j int) bool {
		return normalized.Entries[i].Key < normalized.Entries[j].Key
	})

	encoded, err := json.MarshalIndent(normalized, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode manifest: %w", err)
	}
	return append(encoded, '\n'), nil
}

// DecodeManifest parses and validates a manifest. A version mismatch fails loudly
// instead of being guessed at, because a restore must not silently misread a
// future format.
func DecodeManifest(data []byte) (Manifest, error) {
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode manifest: %w", err)
	}
	if manifest.Version != manifestVersion {
		return Manifest{}, fmt.Errorf(
			"unsupported manifest version %d, expected %d", manifest.Version, manifestVersion,
		)
	}
	if manifest.SnapshotID == "" {
		return Manifest{}, errors.New("manifest is missing snapshot_id")
	}
	if manifest.SourceBucket == "" {
		return Manifest{}, errors.New("manifest is missing source_bucket")
	}
	if manifest.BackupBucket == "" {
		return Manifest{}, errors.New("manifest is missing backup_bucket")
	}
	for _, entry := range manifest.Entries {
		if entry.Key == "" {
			return Manifest{}, errors.New("manifest contains an entry with an empty key")
		}
		if len(entry.SHA256) != sha256.Size*2 {
			return Manifest{}, fmt.Errorf("entry %q has an invalid sha256", entry.Key)
		}
	}
	return manifest, nil
}

func checksum(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func newSnapshotID(now time.Time) string {
	return now.UTC().Format("20060102T150405Z")
}
