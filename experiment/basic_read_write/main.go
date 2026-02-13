package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	badger "github.com/dgraph-io/badger/v4"
)

type Config struct {
	S3Endpoint      string `json:"s3Endpoint"`
	S3Bucket        string `json:"s3Bucket"`
	S3Prefix        string `json:"s3Prefix"`
	S3Region        string `json:"s3Region"`
	S3UsePathStyle  bool   `json:"s3UsePathStyle"`
	Dir             string `json:"dir"`
	ValueDir        string `json:"valueDir"`
	EvictionPolicy  string `json:"evictionPolicy"`
	KeepLocalClosed int    `json:"keepLocalClosed"`
	PruneLocal      bool   `json:"pruneLocal"`

	MinIOAccessKey string `json:"minioAccessKey"`
	MinIOSecretKey string `json:"minioSecretKey"`
}

func loadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config %q: %w", path, err)
	}
	configDir := filepath.Dir(path)
	if absConfigDir, err := filepath.Abs(configDir); err == nil {
		configDir = absConfigDir
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config %q: %w", path, err)
	}

	if cfg.S3Endpoint == "" {
		return Config{}, errors.New("config field \"s3Endpoint\" is required")
	}
	if cfg.S3Bucket == "" {
		return Config{}, errors.New("config field \"s3Bucket\" is required")
	}
	if cfg.Dir != "" && cfg.ValueDir == "" {
		cfg.ValueDir = cfg.Dir
	}
	if cfg.ValueDir != "" && cfg.Dir == "" {
		cfg.Dir = cfg.ValueDir
	}
	if cfg.Dir != "" && !filepath.IsAbs(cfg.Dir) {
		cfg.Dir = filepath.Clean(filepath.Join(configDir, cfg.Dir))
	}
	if cfg.ValueDir != "" && !filepath.IsAbs(cfg.ValueDir) {
		cfg.ValueDir = filepath.Clean(filepath.Join(configDir, cfg.ValueDir))
	}
	switch cfg.EvictionPolicy {
	case "", "fifo", "lru", "lfu":
	default:
		return Config{}, errors.New("config field \"evictionPolicy\" must be one of: fifo, lru, lfu")
	}
	if cfg.KeepLocalClosed < 0 {
		return Config{}, errors.New("config field \"keepLocalClosed\" must be >= 0")
	}
	return cfg, nil
}

func ensureBucket(ctx context.Context, cfg Config) error {
	region := strings.TrimSpace(cfg.S3Region)
	if region == "" {
		region = "us-east-1"
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
	if err != nil {
		return fmt.Errorf("load aws config: %w", err)
	}
	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.UsePathStyle = cfg.S3UsePathStyle
		if endpoint := strings.TrimSpace(cfg.S3Endpoint); endpoint != "" {
			o.BaseEndpoint = aws.String(endpoint)
		}
	})
	_, err = client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(cfg.S3Bucket)})
	if err == nil || strings.Contains(err.Error(), "BucketAlreadyOwnedByYou") || strings.Contains(err.Error(), "BucketAlreadyExists") {
		return nil
	}
	return fmt.Errorf("create bucket %q: %w", cfg.S3Bucket, err)
}

func buildObjectStore(cfg Config) (badger.ValueLogObjectStore, error) {
	return badger.NewS3ValueLogObjectStoreWithConfig(context.Background(), badger.S3ValueLogObjectStoreConfig{
		Bucket:       cfg.S3Bucket,
		Prefix:       cfg.S3Prefix,
		Region:       cfg.S3Region,
		Endpoint:     cfg.S3Endpoint,
		UsePathStyle: cfg.S3UsePathStyle,
	})
}

func buildOffloadPolicy(cfg Config) badger.ValueLogOffloadPolicy {
	switch cfg.EvictionPolicy {
	case "fifo":
		return &badger.FIFOValueLogOffloadPolicy{
			KeepLocalClosed: cfg.KeepLocalClosed,
			PruneLocal:      cfg.PruneLocal,
		}
	case "lru":
		return &badger.LRUValueLogOffloadPolicy{
			KeepLocalClosed: cfg.KeepLocalClosed,
			PruneLocal:      cfg.PruneLocal,
		}
	case "lfu":
		return &badger.LFUValueLogOffloadPolicy{
			KeepLocalClosed: cfg.KeepLocalClosed,
			PruneLocal:      cfg.PruneLocal,
		}
	default:
		return nil
	}
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintf(os.Stderr, "usage: %s <config-json-path>\n", filepath.Base(os.Args[0]))
		os.Exit(2)
	}

	cfg, err := loadConfig(os.Args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}

	if cfg.MinIOAccessKey != "" {
		_ = os.Setenv("AWS_ACCESS_KEY_ID", cfg.MinIOAccessKey)
	}
	if cfg.MinIOSecretKey != "" {
		_ = os.Setenv("AWS_SECRET_ACCESS_KEY", cfg.MinIOSecretKey)
	}
	if err := ensureBucket(context.Background(), cfg); err != nil {
		fmt.Fprintf(os.Stderr, "ensure bucket failed: %v\n", err)
		os.Exit(1)
	}

	dir := cfg.Dir
	valueDir := cfg.ValueDir
	useTemp := false
	if dir == "" {
		dir = filepath.Join(os.TempDir(), fmt.Sprintf("badger-basic-rw-lsm-%d", time.Now().UnixNano()))
		valueDir = dir
		useTemp = true
	}
	store, err := buildObjectStore(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "build s3 object store failed: %v\n", err)
		os.Exit(1)
	}

	opts := badger.DefaultOptions(dir).
		WithValueDir(valueDir).
		WithValueLogOnObjectStorage(true).
		WithValueLogObjectStore(store).
		WithValueThreshold(1).
		WithValueLogMaxEntries(1).
		WithLogger(nil)
	if policy := buildOffloadPolicy(cfg); policy != nil {
		opts = opts.WithValueLogOffloadPolicy(policy)
	}

	db, err := badger.Open(opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open badger: %v\n", err)
		os.Exit(1)
	}
	defer func() {
		_ = db.Close()
		if useTemp {
			_ = os.RemoveAll(dir)
		}
	}()

	const key1 = "basic/read_write/key1"
	const key2 = "basic/read_write/key2"
	const key3 = "basic/read_write/key3"
	value1 := fmt.Sprintf("ok endpoint=%s key=1", cfg.S3Endpoint)
	value2 := fmt.Sprintf("ok endpoint=%s key=2", cfg.S3Endpoint)
	value3 := fmt.Sprintf("ok endpoint=%s key=3", cfg.S3Endpoint)

	if err := db.Update(func(txn *badger.Txn) error {
		return txn.Set([]byte(key1), []byte(value1))
	}); err != nil {
		fmt.Fprintf(os.Stderr, "write key1 failed: %v\n", err)
		os.Exit(1)
	}
	if err := db.Update(func(txn *badger.Txn) error {
		return txn.Set([]byte(key2), []byte(value2))
	}); err != nil {
		fmt.Fprintf(os.Stderr, "write key2 failed: %v\n", err)
		os.Exit(1)
	}
	if err := db.Update(func(txn *badger.Txn) error {
		return txn.Set([]byte(key3), []byte(value3))
	}); err != nil {
		fmt.Fprintf(os.Stderr, "write key3 failed: %v\n", err)
		os.Exit(1)
	}

	var got string
	if err := db.View(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte(key1))
		if err != nil {
			return err
		}
		return item.Value(func(v []byte) error {
			got = string(v)
			return nil
		})
	}); err != nil {
		fmt.Fprintf(os.Stderr, "read key1 failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("basic_read_write success\n")
	fmt.Printf("s3Endpoint=%s\n", cfg.S3Endpoint)
	fmt.Printf("s3Bucket=%s\n", cfg.S3Bucket)
	fmt.Printf("s3Prefix=%s\n", cfg.S3Prefix)
	fmt.Printf("s3Region=%s\n", cfg.S3Region)
	fmt.Printf("s3UsePathStyle=%t\n", cfg.S3UsePathStyle)
	fmt.Printf("dir=%s\n", dir)
	fmt.Printf("valueDir=%s\n", valueDir)
	fmt.Printf("valueLogOnObjectStorage=%t\n", true)
	fmt.Printf("autoOffloadPolicy=%s\n", cfg.EvictionPolicy)
	fmt.Printf("evictionPolicy=%s\n", cfg.EvictionPolicy)
	fmt.Printf("keepLocalClosed=%d\n", cfg.KeepLocalClosed)
	fmt.Printf("pruneLocal=%t\n", cfg.PruneLocal)
	fmt.Printf("readKey=%s\n", key1)
	fmt.Printf("readValue=%s\n", got)
}
