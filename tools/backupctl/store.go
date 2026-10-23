package main

import "context"

// ObjectInfo is the metadata the backup manifest needs. It deliberately carries
// no opaque provider fields so a manifest stays readable and portable.
type ObjectInfo struct {
	Key         string
	Size        int64
	ContentType string
}

// Store is the object-storage port for this tool. Every command depends on this
// interface, so the backup, restore, and verification paths are tested against an
// in-memory implementation instead of a live server.
type Store interface {
	EnsureBucket(ctx context.Context, bucket string) error
	List(ctx context.Context, bucket, prefix string) ([]ObjectInfo, error)
	Get(ctx context.Context, bucket, key string) ([]byte, ObjectInfo, error)
	Put(ctx context.Context, bucket, key, contentType string, data []byte) error
	Delete(ctx context.Context, bucket, key string) error
}
