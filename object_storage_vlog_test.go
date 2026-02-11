/*
 * SPDX-FileCopyrightText: © 2017-2025 Istari Digital, Inc.
 * SPDX-License-Identifier: Apache-2.0
 */

package badger

import (
	"context"
	"encoding/json"
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

func mustGetValue(t *testing.T, db *DB, key []byte) []byte {
	t.Helper()

	var got []byte
	require.NoError(t, db.View(func(txn *Txn) error {
		it, err := txn.Get(key)
		if err != nil {
			return err
		}
		return it.Value(func(v []byte) error {
			got = append([]byte{}, v...)
			return nil
		})
	}))
	return got
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

func TestOffloadPruneReopenAutoHydrateE2E(t *testing.T) {
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
	require.NoError(t, db.Close())

	db, err = Open(opts)
	require.NoError(t, err)
	defer func() { require.NoError(t, db.Close()) }()

	// The index must be restored from disk after reopen.
	db.vlog.filesLock.RLock()
	_, localExists := db.vlog.filesMap[targetFid]
	objectKey, remoteExists := db.vlog.remoteIndex[targetFid]
	db.vlog.filesLock.RUnlock()
	require.False(t, localExists)
	require.True(t, remoteExists)
	require.Equal(t, "000001.vlog", objectKey)

	indexPath := filepath.Join(vlogDir, vlogRemoteIndexFile)
	indexData, err := os.ReadFile(indexPath)
	require.NoError(t, err)
	var payload struct {
		Files map[string]string `json:"files"`
	}
	require.NoError(t, json.Unmarshal(indexData, &payload))
	require.Equal(t, "000001.vlog", payload.Files["1"])

	got := mustGetValue(t, db, []byte("k1"))
	require.Equal(t, []byte("v1-large-enough-for-vlog"), got)

	_, err = os.Stat(db.vlog.fpath(targetFid))
	require.NoError(t, err)
}

func TestHydrateAPIReopenE2E(t *testing.T) {
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
	require.NoError(t, db.Close())

	db, err = Open(opts)
	require.NoError(t, err)
	defer func() { require.NoError(t, db.Close()) }()

	require.NoError(t, db.HydrateValueLogFile(targetFid))
	_, err = os.Stat(db.vlog.fpath(targetFid))
	require.NoError(t, err)

	got := mustGetValue(t, db, []byte("k1"))
	require.Equal(t, []byte("v1-large-enough-for-vlog"), got)
}

func TestHotTierEvictionPolicyFIFO(t *testing.T) {
	p := &FIFOValueLogOffloadPolicy{
		KeepLocalClosed: 1,
		PruneLocal:      true,
	}
	p.OnLocalFileCreated(1)
	p.OnLocalFileCreated(2)
	p.OnLocalFileCreated(3)
	p.OnLocalFileCreated(4)

	decisions := p.DecideOffload(ValueLogOffloadContext{
		NewWritableFid: 4,
		MaxFid:         4,
		LocalFids:      []uint32{1, 2, 3, 4},
	})
	require.Equal(t, []ValueLogOffloadDecision{{Fid: 1, PruneLocal: true}, {Fid: 2, PruneLocal: true}}, decisions)
}

func TestHotTierEvictionPolicyLRU(t *testing.T) {
	p := &LRUValueLogOffloadPolicy{
		KeepLocalClosed: 1,
		PruneLocal:      true,
	}
	p.OnLocalFileCreated(1)
	p.OnLocalFileCreated(2)
	p.OnLocalFileCreated(3)
	p.OnLocalFileCreated(4)
	// Make 2 and 3 newer than 1.
	p.OnLocalFileRead(2)
	p.OnLocalFileRead(3)

	decisions := p.DecideOffload(ValueLogOffloadContext{
		NewWritableFid: 4,
		MaxFid:         4,
		LocalFids:      []uint32{1, 2, 3, 4},
	})
	require.Equal(t, []ValueLogOffloadDecision{{Fid: 1, PruneLocal: true}, {Fid: 2, PruneLocal: true}}, decisions)
}

func TestHotTierEvictionPolicyLFU(t *testing.T) {
	p := &LFUValueLogOffloadPolicy{
		KeepLocalClosed: 1,
		PruneLocal:      true,
	}
	p.OnLocalFileCreated(1)
	p.OnLocalFileCreated(2)
	p.OnLocalFileCreated(3)
	p.OnLocalFileCreated(4)
	// access(1)=3, access(2)=1, access(3)=2
	p.OnLocalFileRead(1)
	p.OnLocalFileRead(1)
	p.OnLocalFileRead(1)
	p.OnLocalFileRead(2)
	p.OnLocalFileRead(3)
	p.OnLocalFileRead(3)

	decisions := p.DecideOffload(ValueLogOffloadContext{
		NewWritableFid: 4,
		MaxFid:         4,
		LocalFids:      []uint32{1, 2, 3, 4},
	})
	require.Equal(t, []ValueLogOffloadDecision{{Fid: 2, PruneLocal: true}, {Fid: 3, PruneLocal: true}}, decisions)
}

func TestAutoOffloadOnRotateE2E(t *testing.T) {
	base := t.TempDir()
	lsmDir := filepath.Join(base, "lsm")
	vlogDir := filepath.Join(base, "vlog")
	store := newMemObjectStore()

	opts := DefaultOptions(lsmDir).
		WithValueDir(vlogDir).
		WithValueLogOnObjectStorage(true).
		WithValueLogObjectStore(store).
		WithValueLogOffloadPolicy(&FIFOValueLogOffloadPolicy{
			KeepLocalClosed: 0,
			PruneLocal:      true,
		}).
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

	db.vlog.filesLock.RLock()
	_, localExists := db.vlog.filesMap[1]
	objectKey, remoteExists := db.vlog.remoteIndex[1]
	db.vlog.filesLock.RUnlock()
	require.False(t, localExists)
	require.True(t, remoteExists)
	require.Equal(t, "000001.vlog", objectKey)
	_, uploaded := store.objects["000001.vlog"]
	require.True(t, uploaded)

	got := mustGetValue(t, db, []byte("k1"))
	require.Equal(t, []byte("v1-large-enough-for-vlog"), got)
}
