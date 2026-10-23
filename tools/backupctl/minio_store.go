package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// MinIOStore is the S3-compatible adapter. It is the only place in this tool that
// knows a provider, so the backup, verify, and restore logic stays portable.
type MinIOStore struct {
	client *minio.Client
}

// NewMinIOStore connects lazily; the first command reports any endpoint or
// credential problem with the operation that needed it.
func NewMinIOStore(endpoint, accessKey, secretKey string, secure bool) (*MinIOStore, error) {
	if endpoint == "" {
		return nil, errors.New("endpoint is required")
	}
	if accessKey == "" || secretKey == "" {
		return nil, errors.New("access key and secret key are required")
	}
	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: secure,
	})
	if err != nil {
		return nil, fmt.Errorf("create S3 client: %w", err)
	}
	return &MinIOStore{client: client}, nil
}

// EnsureBucket creates a missing bucket and accepts an already-owned one.
func (s *MinIOStore) EnsureBucket(ctx context.Context, bucket string) error {
	exists, err := s.client.BucketExists(ctx, bucket)
	if err != nil {
		return fmt.Errorf("check bucket %q: %w", bucket, err)
	}
	if exists {
		return nil
	}
	if err := s.client.MakeBucket(ctx, bucket, minio.MakeBucketOptions{}); err != nil {
		return fmt.Errorf("create bucket %q: %w", bucket, err)
	}
	return nil
}

// List returns every object below the prefix in stable key order.
func (s *MinIOStore) List(ctx context.Context, bucket, prefix string) ([]ObjectInfo, error) {
	objects := make([]ObjectInfo, 0)
	stream := s.client.ListObjects(ctx, bucket, minio.ListObjectsOptions{
		Prefix:    prefix,
		Recursive: true,
	})
	for object := range stream {
		if object.Err != nil {
			return nil, fmt.Errorf("list bucket %q: %w", bucket, object.Err)
		}
		objects = append(objects, ObjectInfo{
			Key:         object.Key,
			Size:        object.Size,
			ContentType: object.ContentType,
		})
	}
	return objects, nil
}

// Get reads an object fully. Media objects are bounded by the service upload
// limit, so a full read is the simplest way to checksum them.
func (s *MinIOStore) Get(ctx context.Context, bucket, key string) ([]byte, ObjectInfo, error) {
	object, err := s.client.GetObject(ctx, bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, ObjectInfo{}, fmt.Errorf("get object %q: %w", key, err)
	}
	defer func() { _ = object.Close() }()

	stat, err := object.Stat()
	if err != nil {
		return nil, ObjectInfo{}, fmt.Errorf("stat object %q: %w", key, err)
	}
	data, err := io.ReadAll(object)
	if err != nil {
		return nil, ObjectInfo{}, fmt.Errorf("read object %q: %w", key, err)
	}
	return data, ObjectInfo{
		Key:         key,
		Size:        int64(len(data)),
		ContentType: stat.ContentType,
	}, nil
}

// Put writes one object with an explicit content type and exact size.
func (s *MinIOStore) Put(ctx context.Context, bucket, key, contentType string, data []byte) error {
	reader := bytes.NewReader(data)
	_, err := s.client.PutObject(ctx, bucket, key, reader, int64(len(data)), minio.PutObjectOptions{
		ContentType: contentType,
	})
	if err != nil {
		return fmt.Errorf("put object %q: %w", key, err)
	}
	return nil
}

// Delete removes one object; a missing key is not an error.
func (s *MinIOStore) Delete(ctx context.Context, bucket, key string) error {
	if err := s.client.RemoveObject(ctx, bucket, key, minio.RemoveObjectOptions{}); err != nil {
		return fmt.Errorf("delete object %q: %w", key, err)
	}
	return nil
}
