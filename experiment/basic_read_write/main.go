package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

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
}

func loadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config %q: %w", path, err)
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

	// Default to temp dirs when not explicitly configured.
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

	const key = "basic/read_write/key"
	value := fmt.Sprintf("ok endpoint=%s", cfg.S3Endpoint)

	if err := db.Update(func(txn *badger.Txn) error {
		return txn.Set([]byte(key), []byte(value))
	}); err != nil {
		fmt.Fprintf(os.Stderr, "write failed: %v\n", err)
		os.Exit(1)
	}

	var got string
	if err := db.View(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte(key))
		if err != nil {
			return err
		}
		return item.Value(func(v []byte) error {
			got = string(v)
			return nil
		})
	}); err != nil {
		fmt.Fprintf(os.Stderr, "read failed: %v\n", err)
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
	fmt.Printf("evictionPolicy=%s\n", cfg.EvictionPolicy)
	fmt.Printf("keepLocalClosed=%d\n", cfg.KeepLocalClosed)
	fmt.Printf("pruneLocal=%t\n", cfg.PruneLocal)
	fmt.Printf("key=%s\n", key)
	fmt.Printf("value=%s\n", got)
}
