/*
 * SPDX-FileCopyrightText: © 2017-2025 Istari Digital, Inc.
 * SPDX-License-Identifier: Apache-2.0
 */

package badger

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

type memObjectStore struct {
	objects map[string][]byte
}

func newMemObjectStore() *memObjectStore {
	return &memObjectStore{objects: make(map[string][]byte)}
}

func (m *memObjectStore) UploadFile(_ context.Context, localPath string, objectKey string) error {
	data, err := os.ReadFile(localPath)
	if err != nil {
		return err
	}
	m.objects[objectKey] = data
	return nil
}

func (m *memObjectStore) DownloadFile(_ context.Context, objectKey string, localPath string) error {
	data, ok := m.objects[objectKey]
	if !ok {
		return ErrValueLogFileNotFound
	}
	return os.WriteFile(localPath, data, 0600)
}

func (m *memObjectStore) DeleteObject(_ context.Context, objectKey string) error {
	delete(m.objects, objectKey)
	return nil
}

func TestSyncDirDoesNotSkipObjectStorageValueDir(t *testing.T) {
	base := t.TempDir()
	lsmDir := filepath.Join(base, "lsm")
	objectStorageMountValueDir := filepath.Join(base, "mounted-vlog")
	require.NoError(t, os.MkdirAll(lsmDir, 0700))
	require.NoError(t, os.MkdirAll(objectStorageMountValueDir, 0700))

	db := &DB{
		opt: DefaultOptions(lsmDir).
			WithValueDir(objectStorageMountValueDir).
			WithValueLogOnObjectStorage(true),
	}

	// ValueDir is still a local directory and should be synced.
	require.NoError(t, db.syncDir(db.opt.ValueDir))
	// Dir should also be synced.
	require.NoError(t, db.syncDir(db.opt.Dir))
}

func TestSyncDirWhenValueDirEqualsDirInObjectMode(t *testing.T) {
	base := t.TempDir()
	sameDir := filepath.Join(base, "same")
	require.NoError(t, os.MkdirAll(sameDir, 0700))

	db := &DB{
		opt: DefaultOptions(sameDir).WithValueLogOnObjectStorage(true),
	}

	// Even in object mode, syncDir should still sync local dir.
	require.NoError(t, db.syncDir(sameDir))
}

func TestWithValueDirAndObjectStorageMode(t *testing.T) {
	opt := DefaultOptions("/tmp/badger").
		WithValueDir("/mnt/object-store-vlog").
		WithValueLogOnObjectStorage(true)

	require.Equal(t, "/mnt/object-store-vlog", opt.ValueDir)
	require.True(t, opt.ValueLogOnObjectStorage)
}

func TestWithValueLogOnObjectStorage(t *testing.T) {
	opt := DefaultOptions("/tmp/badger").WithValueLogOnObjectStorage(true)
	require.True(t, opt.ValueLogOnObjectStorage)
}

func TestCheckAndSetOptionsRejectsURLAsValueDir(t *testing.T) {
	opt := DefaultOptions("/tmp/badger").
		WithValueDir("s3://my-bucket/badger/vlog").
		WithValueLogOnObjectStorage(true)

	err := checkAndSetOptions(&opt)
	require.Error(t, err)
	require.ErrorContains(t, err, "ValueDir cannot be an object-storage URL")
}

func TestCheckAndSetOptionsAcceptsObjectStorageMode(t *testing.T) {
	opt := DefaultOptions("/tmp/badger").
		WithValueDir("/mnt/object-store-vlog").
		WithValueLogOnObjectStorage(true)

	require.NoError(t, checkAndSetOptions(&opt))
}

func TestOffloadAndAutoHydrateRead(t *testing.T) {
	base := t.TempDir()
	lsmDir := filepath.Join(base, "lsm")
	vlogDir := filepath.Join(base, "vlog")
	store := newMemObjectStore()

	opts := DefaultOptions(lsmDir).
		WithValueDir(vlogDir).
		WithValueLogOnObjectStorage(true).
		WithValueLogObjectStore(store).
		WithValueThreshold(1).
		WithValueLogMaxEntries(1)

	db, err := Open(opts)
	require.NoError(t, err)
	defer func() { require.NoError(t, db.Close()) }()

	require.NoError(t, db.Update(func(txn *Txn) error {
		return txn.Set([]byte("k1"), []byte("v1-large-enough-for-vlog"))
	}))
	require.NoError(t, db.Update(func(txn *Txn) error {
		return txn.Set([]byte("k2"), []byte("v2-large-enough-for-vlog"))
	}))

	fids := db.vlog.sortedFids()
	require.GreaterOrEqual(t, len(fids), 2)
	targetFid := fids[0]
	require.NoError(t, db.OffloadValueLogFile(targetFid, true))

	_, err = os.Stat(db.vlog.fpath(targetFid))
	require.Error(t, err)

	var got []byte
	require.NoError(t, db.View(func(txn *Txn) error {
		it, err := txn.Get([]byte("k1"))
		if err != nil {
			return err
		}
		return it.Value(func(v []byte) error {
			got = append([]byte{}, v...)
			return nil
		})
	}))
	require.Equal(t, []byte("v1-large-enough-for-vlog"), got)

	_, err = os.Stat(db.vlog.fpath(targetFid))
	require.NoError(t, err)
}
