/*
 * SPDX-FileCopyrightText: © 2017-2025 Istari Digital, Inc.
 * SPDX-License-Identifier: Apache-2.0
 */

package cmd

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	humanize "github.com/dustin/go-humanize"
	"github.com/spf13/cobra"

	"github.com/dgraph-io/badger/v4"
	"github.com/dgraph-io/badger/v4/options"
	"github.com/dgraph-io/badger/v4/y"
)

var ycsbBenchCmd = &cobra.Command{
	Use:   "ycsb",
	Short: "Run YCSB workloads (A-F) against Badger.",
	Long: `Run YCSB-style benchmark workloads against Badger.

This command supports workload A-F with configurable load and run phases.
It is useful for validating mixed read/write workloads with Zipfian key access.`,
	RunE: ycsbBench,
}

type ycsbOperation uint8

const (
	ycsbRead ycsbOperation = iota
	ycsbUpdate
	ycsbInsert
	ycsbScan
	ycsbReadModifyWrite
	ycsbReadLatest
	numYCSBOperations
)

func (op ycsbOperation) String() string {
	switch op {
	case ycsbRead:
		return "READ"
	case ycsbUpdate:
		return "UPDATE"
	case ycsbInsert:
		return "INSERT"
	case ycsbScan:
		return "SCAN"
	case ycsbReadModifyWrite:
		return "READ_MODIFY_WRITE"
	case ycsbReadLatest:
		return "READ_LATEST"
	default:
		return "UNKNOWN"
	}
}

type ycsbMix struct {
	weights [numYCSBOperations]int
	total   int
}

func (m ycsbMix) pick(r *rand.Rand) ycsbOperation {
	if m.total <= 0 {
		return ycsbRead
	}
	x := r.Intn(m.total)
	sum := 0
	for i, w := range m.weights {
		sum += w
		if x < sum {
			return ycsbOperation(i)
		}
	}
	return ycsbRead
}

func workloadMix(workload string) (ycsbMix, error) {
	var mix ycsbMix
	switch strings.ToUpper(strings.TrimSpace(workload)) {
	case "A":
		mix.weights[ycsbRead] = 50
		mix.weights[ycsbUpdate] = 50
	case "B":
		mix.weights[ycsbRead] = 95
		mix.weights[ycsbUpdate] = 5
	case "C":
		mix.weights[ycsbRead] = 100
	case "D":
		mix.weights[ycsbReadLatest] = 95
		mix.weights[ycsbInsert] = 5
	case "E":
		mix.weights[ycsbScan] = 95
		mix.weights[ycsbInsert] = 5
	case "F":
		mix.weights[ycsbRead] = 50
		mix.weights[ycsbReadModifyWrite] = 50
	default:
		return ycsbMix{}, fmt.Errorf("unsupported workload %q (expected one of A,B,C,D,E,F)", workload)
	}
	for _, w := range mix.weights {
		mix.total += w
	}
	if mix.total == 0 {
		return ycsbMix{}, errors.New("empty workload mix")
	}
	return mix, nil
}

var ycsbOpts = struct {
	workload   string
	goroutines int

	recordCount    uint64
	operationCount uint64
	duration       string
	skipLoad       bool

	keySize   int
	valueSize int
	scanCount int
	latestN   int

	seed       int64
	zipfianS   float64
	zipfianV   float64
	syncWrites bool
	showLogs   bool
	zstdComp   bool

	valueThreshold int64
	blockCacheSize int64
	indexCacheSize int64
}{
	workload:       "A",
	goroutines:     16,
	recordCount:    1_000_000,
	operationCount: 1_000_000,
	duration:       "0s",
	skipLoad:       false,
	keySize:        24,
	valueSize:      1000,
	scanCount:      100,
	latestN:        1024,
	seed:           1,
	zipfianS:       1.1,
	zipfianV:       1.0,
	syncWrites:     false,
	showLogs:       false,
	zstdComp:       false,
	valueThreshold: 1024,
	blockCacheSize: 256,
	indexCacheSize: 0,
}

type ycsbMetrics struct {
	opCount   [numYCSBOperations]atomic.Uint64
	latencyNS [numYCSBOperations]atomic.Uint64
	bytesRead atomic.Uint64
	errors    atomic.Uint64
}

func (m *ycsbMetrics) record(op ycsbOperation, dur time.Duration, bytes uint64, err error) {
	if err != nil {
		m.errors.Add(1)
		return
	}
	m.opCount[op].Add(1)
	m.latencyNS[op].Add(uint64(dur))
	if bytes > 0 {
		m.bytesRead.Add(bytes)
	}
}

func (m *ycsbMetrics) totalOps() uint64 {
	var total uint64
	for i := range m.opCount {
		total += m.opCount[i].Load()
	}
	return total
}

func init() {
	benchCmd.AddCommand(ycsbBenchCmd)
	ycsbBenchCmd.Flags().StringVarP(&ycsbOpts.workload, "workload", "w", "A",
		"YCSB workload to run: A, B, C, D, E, or F")
	ycsbBenchCmd.Flags().Uint64Var(&ycsbOpts.recordCount, "records", 1_000_000,
		"Number of records to load before run phase")
	ycsbBenchCmd.Flags().Uint64Var(&ycsbOpts.operationCount, "ops", 1_000_000,
		"Number of operations to execute in run phase (0 means duration-based run)")
	ycsbBenchCmd.Flags().StringVarP(&ycsbOpts.duration, "duration", "d", "0s",
		"Run duration (used when --ops=0, or as an upper bound when --ops>0)")
	ycsbBenchCmd.Flags().BoolVar(&ycsbOpts.skipLoad, "skip-load", false,
		"Skip load phase and run benchmark on existing data")
	ycsbBenchCmd.Flags().IntVarP(&ycsbOpts.goroutines, "goroutines", "g", 16,
		"Number of concurrent goroutines in run phase")
	ycsbBenchCmd.Flags().IntVarP(&ycsbOpts.keySize, "key-size", "k", 24,
		"Key size in bytes (must be at least 8)")
	ycsbBenchCmd.Flags().IntVar(&ycsbOpts.valueSize, "value-size", 1000,
		"Value size in bytes")
	ycsbBenchCmd.Flags().IntVar(&ycsbOpts.scanCount, "scan-count", 100,
		"Number of records to scan per SCAN operation (workload E)")
	ycsbBenchCmd.Flags().IntVar(&ycsbOpts.latestN, "latest-n", 1024,
		"Recent key window size for READ_LATEST operations (workload D)")
	ycsbBenchCmd.Flags().Int64Var(&ycsbOpts.seed, "seed", 1,
		"Random seed")
	ycsbBenchCmd.Flags().Float64Var(&ycsbOpts.zipfianS, "zipf-s", 1.1,
		"Zipfian distribution parameter s (>1)")
	ycsbBenchCmd.Flags().Float64Var(&ycsbOpts.zipfianV, "zipf-v", 1.0,
		"Zipfian distribution parameter v (>=1)")
	ycsbBenchCmd.Flags().BoolVar(&ycsbOpts.syncWrites, "sync", false,
		"Sync writes to disk")
	ycsbBenchCmd.Flags().BoolVarP(&ycsbOpts.showLogs, "verbose", "v", false,
		"Show Badger logs")
	ycsbBenchCmd.Flags().BoolVar(&ycsbOpts.zstdComp, "zstd", false,
		"Use ZSTD compression")
	ycsbBenchCmd.Flags().Int64VarP(&ycsbOpts.valueThreshold, "value-th", "t", 1<<10,
		"Value threshold")
	ycsbBenchCmd.Flags().Int64Var(&ycsbOpts.blockCacheSize, "block-cache-mb", 256,
		"Block cache size in MB")
	ycsbBenchCmd.Flags().Int64Var(&ycsbOpts.indexCacheSize, "index-cache-mb", 0,
		"Index cache size in MB")
}

func ycsbBench(cmd *cobra.Command, args []string) error {
	if ycsbOpts.goroutines <= 0 {
		return errors.New("goroutines must be > 0")
	}
	if ycsbOpts.keySize < 8 {
		return errors.New("key-size must be >= 8")
	}
	if ycsbOpts.valueSize <= 0 {
		return errors.New("value-size must be > 0")
	}
	if ycsbOpts.scanCount <= 0 {
		return errors.New("scan-count must be > 0")
	}
	if ycsbOpts.latestN <= 0 {
		return errors.New("latest-n must be > 0")
	}
	if ycsbOpts.zipfianS <= 1.0 {
		return errors.New("zipf-s must be > 1")
	}
	if ycsbOpts.zipfianV < 1.0 {
		return errors.New("zipf-v must be >= 1")
	}

	mix, err := workloadMix(ycsbOpts.workload)
	if err != nil {
		return err
	}

	runFor, err := time.ParseDuration(ycsbOpts.duration)
	if err != nil {
		return y.Wrapf(err, "unable to parse duration")
	}
	if ycsbOpts.operationCount == 0 && runFor <= 0 {
		return errors.New("either --ops must be > 0 or --duration must be > 0")
	}

	opt := badger.DefaultOptions(sstDir).
		WithValueDir(vlogDir).
		WithSyncWrites(ycsbOpts.syncWrites).
		WithValueThreshold(ycsbOpts.valueThreshold).
		WithBlockCacheSize(ycsbOpts.blockCacheSize << 20).
		WithIndexCacheSize(ycsbOpts.indexCacheSize << 20).
		WithDetectConflicts(true)
	if ycsbOpts.zstdComp {
		opt = opt.WithCompression(options.ZSTD)
	}
	if !ycsbOpts.showLogs {
		opt = opt.WithLogger(nil)
	}

	fmt.Printf("Opening badger with options = %+v\n", opt)
	db, err := badger.Open(opt)
	if err != nil {
		return y.Wrapf(err, "unable to open DB")
	}
	defer db.Close()

	nextKeyID := atomic.Uint64{}
	if ycsbOpts.skipLoad {
		nextKeyID.Store(ycsbOpts.recordCount)
	} else {
		fmt.Println("*********************************************************")
		fmt.Printf("YCSB load phase started. records=%d key_size=%d value_size=%d\n",
			ycsbOpts.recordCount, ycsbOpts.keySize, ycsbOpts.valueSize)
		fmt.Println("*********************************************************")
		if err := ycsbLoad(db, ycsbOpts.recordCount); err != nil {
			return err
		}
		nextKeyID.Store(ycsbOpts.recordCount)
	}

	fmt.Println("*********************************************************")
	fmt.Printf("YCSB run phase started. workload=%s goroutines=%d ops=%d duration=%s\n",
		strings.ToUpper(ycsbOpts.workload), ycsbOpts.goroutines, ycsbOpts.operationCount, runFor)
	fmt.Println("*********************************************************")

	metrics := &ycsbMetrics{}
	ctx, cancel := ycsbRunContext(runFor)
	defer cancel()

	var issued atomic.Uint64
	runStart := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < ycsbOpts.goroutines; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			ycsbRunWorker(ctx, db, workerID, mix, metrics, &nextKeyID, &issued)
		}(i)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				dur := time.Since(runStart)
				ops := metrics.totalOps()
				errCnt := metrics.errors.Load()
				opsRate := uint64(float64(ops) / math.Max(dur.Seconds(), 1))
				fmt.Printf("[YCSB] elapsed=%s ops=%d errors=%d throughput=%d ops/sec\n",
					y.FixedDuration(dur), ops, errCnt, opsRate)
			}
		}
	}()

	wg.Wait()
	cancel()
	<-done

	ycsbPrintSummary(time.Since(runStart), metrics)
	fmt.Println(db.LevelsToString())
	return nil
}

func ycsbLoad(db *badger.DB, records uint64) error {
	if records == 0 {
		return nil
	}

	wb := db.NewWriteBatch()
	defer wb.Cancel()

	progressEvery := uint64(100_000)
	start := time.Now()
	for i := uint64(0); i < records; i++ {
		key := makeYCSBKey(i, ycsbOpts.keySize)
		value := makeYCSBValue(i, ycsbOpts.valueSize)
		if err := wb.Set(key, value); err != nil {
			return y.Wrapf(err, "load phase set failed at key=%d", i)
		}
		if i > 0 && i%progressEvery == 0 {
			dur := time.Since(start)
			opsRate := uint64(float64(i) / math.Max(dur.Seconds(), 1))
			fmt.Printf("[YCSB-LOAD] loaded=%d throughput=%d ops/sec\n", i, opsRate)
		}
	}
	if err := wb.Flush(); err != nil {
		return y.Wrapf(err, "load phase flush failed")
	}
	fmt.Printf("[YCSB-LOAD] done records=%d elapsed=%s\n", records, y.FixedDuration(time.Since(start)))
	return nil
}

func ycsbRunContext(runFor time.Duration) (context.Context, context.CancelFunc) {
	if runFor > 0 {
		return context.WithTimeout(context.Background(), runFor)
	}
	return context.WithCancel(context.Background())
}

func ycsbRunWorker(
	ctx context.Context,
	db *badger.DB,
	workerID int,
	mix ycsbMix,
	metrics *ycsbMetrics,
	nextKeyID *atomic.Uint64,
	issued *atomic.Uint64,
) {
	seed := ycsbOpts.seed + int64(workerID+1)
	r := rand.New(rand.NewSource(seed))

	zipfCap := ycsbOpts.recordCount
	if ycsbOpts.operationCount > 0 {
		zipfCap += ycsbOpts.operationCount
	} else {
		zipfCap += ycsbOpts.recordCount
	}
	if zipfCap == 0 {
		zipfCap = 1
	}
	zipf := rand.NewZipf(r, ycsbOpts.zipfianS, ycsbOpts.zipfianV, zipfCap-1)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if ycsbOpts.operationCount > 0 {
			n := issued.Add(1)
			if n > ycsbOpts.operationCount {
				return
			}
		}

		op := mix.pick(r)
		start := time.Now()
		bytes, err := ycsbDoOperation(db, r, zipf, op, nextKeyID)
		metrics.record(op, time.Since(start), bytes, err)
	}
}

func ycsbDoOperation(
	db *badger.DB,
	r *rand.Rand,
	zipf *rand.Zipf,
	op ycsbOperation,
	nextKeyID *atomic.Uint64,
) (uint64, error) {
	current := nextKeyID.Load()
	if current == 0 {
		return 0, nil
	}

	pickExistingID := func() uint64 {
		c := nextKeyID.Load()
		if c == 0 {
			return 0
		}
		if c == 1 {
			return 0
		}
		return zipf.Uint64() % c
	}

	switch op {
	case ycsbRead:
		return ycsbReadOp(db, pickExistingID())
	case ycsbUpdate:
		return 0, ycsbUpdateOp(db, pickExistingID(), makeYCSBValue(r.Uint64(), ycsbOpts.valueSize))
	case ycsbInsert:
		id := nextKeyID.Add(1) - 1
		return 0, ycsbInsertOp(db, id, makeYCSBValue(id, ycsbOpts.valueSize))
	case ycsbScan:
		return ycsbScanOp(db, pickExistingID(), ycsbOpts.scanCount)
	case ycsbReadModifyWrite:
		return 0, ycsbReadModifyWriteOp(db, pickExistingID(), makeYCSBValue(r.Uint64(), ycsbOpts.valueSize))
	case ycsbReadLatest:
		latest := nextKeyID.Load()
		if latest == 0 {
			return 0, nil
		}
		window := uint64(ycsbOpts.latestN)
		if latest < window {
			window = latest
		}
		id := latest - 1 - uint64(r.Int63n(int64(window)))
		return ycsbReadOp(db, id)
	default:
		return 0, nil
	}
}

func ycsbReadOp(db *badger.DB, id uint64) (uint64, error) {
	key := makeYCSBKey(id, ycsbOpts.keySize)
	var size uint64
	err := db.View(func(txn *badger.Txn) error {
		item, err := txn.Get(key)
		if err != nil {
			return err
		}
		return item.Value(func(v []byte) error {
			size = uint64(len(v))
			return nil
		})
	})
	if err != nil && errors.Is(err, badger.ErrKeyNotFound) {
		return 0, nil
	}
	return size, err
}

func ycsbUpdateOp(db *badger.DB, id uint64, value []byte) error {
	key := makeYCSBKey(id, ycsbOpts.keySize)
	return ycsbRetryOnConflict(func() error {
		return db.Update(func(txn *badger.Txn) error {
			return txn.Set(key, value)
		})
	})
}

func ycsbInsertOp(db *badger.DB, id uint64, value []byte) error {
	key := makeYCSBKey(id, ycsbOpts.keySize)
	return ycsbRetryOnConflict(func() error {
		return db.Update(func(txn *badger.Txn) error {
			return txn.Set(key, value)
		})
	})
}

func ycsbScanOp(db *badger.DB, startID uint64, count int) (uint64, error) {
	key := makeYCSBKey(startID, ycsbOpts.keySize)
	var scanned uint64
	err := db.View(func(txn *badger.Txn) error {
		it := txn.NewIterator(badger.DefaultIteratorOptions)
		defer it.Close()

		seen := 0
		for it.Seek(key); it.Valid() && seen < count; it.Next() {
			item := it.Item()
			scanned += uint64(item.EstimatedSize())
			seen++
		}
		return nil
	})
	return scanned, err
}

func ycsbReadModifyWriteOp(db *badger.DB, id uint64, value []byte) error {
	key := makeYCSBKey(id, ycsbOpts.keySize)
	return ycsbRetryOnConflict(func() error {
		return db.Update(func(txn *badger.Txn) error {
			item, err := txn.Get(key)
			if err != nil && !errors.Is(err, badger.ErrKeyNotFound) {
				return err
			}
			if err == nil {
				if err := item.Value(func(v []byte) error { return nil }); err != nil {
					return err
				}
			}
			return txn.Set(key, value)
		})
	})
}

func ycsbRetryOnConflict(fn func() error) error {
	const retries = 16
	var err error
	for i := 0; i < retries; i++ {
		err = fn()
		if err == nil || !errors.Is(err, badger.ErrConflict) {
			return err
		}
	}
	return err
}

func ycsbPrintSummary(elapsed time.Duration, metrics *ycsbMetrics) {
	ops := metrics.totalOps()
	errs := metrics.errors.Load()
	bytesRead := metrics.bytesRead.Load()
	throughput := uint64(float64(ops) / math.Max(elapsed.Seconds(), 1))

	fmt.Println("*********************************************************")
	fmt.Println("YCSB summary")
	fmt.Println("*********************************************************")
	fmt.Printf("Elapsed        : %s\n", y.FixedDuration(elapsed))
	fmt.Printf("Ops            : %d\n", ops)
	fmt.Printf("Errors         : %d\n", errs)
	fmt.Printf("Throughput     : %d ops/sec\n", throughput)
	fmt.Printf("Bytes read     : %s\n", humanize.IBytes(bytesRead))

	for i := 0; i < int(numYCSBOperations); i++ {
		op := ycsbOperation(i)
		count := metrics.opCount[i].Load()
		if count == 0 {
			continue
		}
		lat := metrics.latencyNS[i].Load()
		avg := time.Duration(lat / count)
		fmt.Printf("%-17s: ops=%d avg-lat=%s\n", op.String(), count, avg)
	}
}

func makeYCSBKey(id uint64, size int) []byte {
	key := make([]byte, size)
	binary.BigEndian.PutUint64(key[size-8:], id)
	return key
}

func makeYCSBValue(seed uint64, size int) []byte {
	value := make([]byte, size)
	binary.BigEndian.PutUint64(value[:8], seed)
	if size > 8 {
		for i := 8; i < size; i++ {
			value[i] = byte(seed + uint64(i))
		}
	}
	return value
}
