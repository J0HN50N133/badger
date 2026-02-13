/*
 * SPDX-FileCopyrightText: © 2017-2025 Istari Digital, Inc.
 * SPDX-License-Identifier: Apache-2.0
 */

package badger

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// ValueLogObjectStore defines the minimal object-store operations needed by
// vlog tiering MVP.
//
// objectKey is a logical object name (for example: 000123.vlog). The concrete
// mapping to bucket/prefix is implementation-defined.
type ValueLogObjectStore interface {
	UploadFile(ctx context.Context, localPath string, objectKey string) error
	DownloadFile(ctx context.Context, objectKey string, localPath string) error
	DeleteObject(ctx context.Context, objectKey string) error
}

type s3ObjectAPI interface {
	PutObject(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	GetObject(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	DeleteObject(ctx context.Context, params *s3.DeleteObjectInput, optFns ...func(*s3.Options)) (*s3.DeleteObjectOutput, error)
}

// S3ValueLogObjectStoreConfig is used to construct an S3-backed
// ValueLogObjectStore.
type S3ValueLogObjectStoreConfig struct {
	Bucket string
	Prefix string
	// Region defaults to "us-east-1" when empty.
	Region string
	// Endpoint is optional and is useful for S3-compatible deployments
	// (for example, JuiceFS backed by MinIO/Ceph).
	Endpoint string
	// UsePathStyle should be true for many S3-compatible services.
	UsePathStyle bool
}

// S3ValueLogObjectStore is an AWS SDK v2 based implementation of
// ValueLogObjectStore.
type S3ValueLogObjectStore struct {
	client s3ObjectAPI
	bucket string
	prefix string
}

// NewS3ValueLogObjectStore creates an S3-based ValueLogObjectStore from an
// existing AWS SDK v2 S3 client.
func NewS3ValueLogObjectStore(client *s3.Client, bucket, prefix string) (*S3ValueLogObjectStore, error) {
	if client == nil {
		return nil, errors.New("s3 client is nil")
	}
	if strings.TrimSpace(bucket) == "" {
		return nil, errors.New("s3 bucket is required")
	}
	return &S3ValueLogObjectStore{
		client: client,
		bucket: bucket,
		prefix: strings.Trim(strings.TrimSpace(prefix), "/"),
	}, nil
}

// NewS3ValueLogObjectStoreWithConfig creates an AWS SDK v2 S3 client and wraps
// it into S3ValueLogObjectStore.
func NewS3ValueLogObjectStoreWithConfig(
	ctx context.Context,
	cfg S3ValueLogObjectStoreConfig,
) (*S3ValueLogObjectStore, error) {
	region := strings.TrimSpace(cfg.Region)
	if region == "" {
		region = "us-east-1"
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}

	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.UsePathStyle = cfg.UsePathStyle
		if endpoint := strings.TrimSpace(cfg.Endpoint); endpoint != "" {
			o.BaseEndpoint = aws.String(endpoint)
		}
	})
	return NewS3ValueLogObjectStore(client, cfg.Bucket, cfg.Prefix)
}

func (s *S3ValueLogObjectStore) objectName(objectKey string) string {
	key := strings.TrimLeft(strings.TrimSpace(objectKey), "/")
	if s.prefix == "" {
		return key
	}
	if key == "" {
		return s.prefix
	}
	return s.prefix + "/" + key
}

func (s *S3ValueLogObjectStore) UploadFile(ctx context.Context, localPath string, objectKey string) error {
	f, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("open upload file %q: %w", localPath, err)
	}
	defer func() { _ = f.Close() }()

	_, err = s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(s.objectName(objectKey)),
		Body:   f,
	})
	if err != nil {
		return fmt.Errorf("put object %q: %w", s.objectName(objectKey), err)
	}
	return nil
}

func (s *S3ValueLogObjectStore) DownloadFile(ctx context.Context, objectKey string, localPath string) error {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(s.objectName(objectKey)),
	})
	if err != nil {
		return fmt.Errorf("get object %q: %w", s.objectName(objectKey), err)
	}
	defer func() { _ = out.Body.Close() }()

	if dir := filepath.Dir(localPath); dir != "." {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return fmt.Errorf("mkdir for download path %q: %w", dir, err)
		}
	}
	f, err := os.OpenFile(localPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("create download file %q: %w", localPath, err)
	}
	if _, err := io.Copy(f, out.Body); err != nil {
		_ = f.Close()
		_ = os.Remove(localPath)
		return fmt.Errorf("write download file %q: %w", localPath, err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(localPath)
		return fmt.Errorf("close download file %q: %w", localPath, err)
	}
	return nil
}

func (s *S3ValueLogObjectStore) DeleteObject(ctx context.Context, objectKey string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(s.objectName(objectKey)),
	})
	if err != nil {
		return fmt.Errorf("delete object %q: %w", s.objectName(objectKey), err)
	}
	return nil
}
