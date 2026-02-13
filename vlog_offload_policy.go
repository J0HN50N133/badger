/*
 * SPDX-FileCopyrightText: © 2017-2025 Istari Digital, Inc.
 * SPDX-License-Identifier: Apache-2.0
 */

package badger

import (
	"container/heap"
	clist "container/list"
	"sort"
	"sync"
)

// ValueLogOffloadContext describes local vlog state at rotation time.
type ValueLogOffloadContext struct {
	NewWritableFid uint32
	MaxFid         uint32
	LocalFids      []uint32
}

// ValueLogOffloadDecision describes which local vlog should be offloaded.
type ValueLogOffloadDecision struct {
	Fid        uint32
	PruneLocal bool
}

// ValueLogOffloadPolicy keeps internal state and decides which closed vlog
// files should be offloaded on rotation.
type ValueLogOffloadPolicy interface {
	OnLocalFileCreated(fid uint32)
	OnLocalFileRead(fid uint32)
	OnLocalFileDeleted(fid uint32)
	DecideOffload(ctx ValueLogOffloadContext) []ValueLogOffloadDecision
}

func closedFids(ctx ValueLogOffloadContext) []uint32 {
	closed := make([]uint32, 0, len(ctx.LocalFids))
	for _, fid := range ctx.LocalFids {
		if fid < ctx.MaxFid {
			closed = append(closed, fid)
		}
	}
	return closed
}

func normalizeKeep(keep int) int {
	if keep < 0 {
		return 0
	}
	return keep
}

func decisionsFromSortedFids(sorted []uint32, keep int, prune bool) []ValueLogOffloadDecision {
	keep = normalizeKeep(keep)
	if len(sorted) <= keep {
		return nil
	}
	n := len(sorted) - keep
	out := make([]ValueLogOffloadDecision, 0, n)
	for _, fid := range sorted[:n] {
		out = append(out, ValueLogOffloadDecision{
			Fid:        fid,
			PruneLocal: prune,
		})
	}
	return out
}

// FIFOValueLogOffloadPolicy evicts oldest closed local vlog files first.
type FIFOValueLogOffloadPolicy struct {
	KeepLocalClosed int
	PruneLocal      bool

	mu    sync.Mutex
	ll    *clist.List
	index map[uint32]*clist.Element
}

func (p *FIFOValueLogOffloadPolicy) ensure() {
	if p.ll == nil {
		p.ll = clist.New()
	}
	if p.index == nil {
		p.index = make(map[uint32]*clist.Element)
	}
}

func (p *FIFOValueLogOffloadPolicy) OnLocalFileCreated(fid uint32) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ensure()
	if _, ok := p.index[fid]; ok {
		return
	}
	p.index[fid] = p.ll.PushBack(fid)
}

func (p *FIFOValueLogOffloadPolicy) OnLocalFileRead(_ uint32) {}

func (p *FIFOValueLogOffloadPolicy) OnLocalFileDeleted(fid uint32) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ensure()
	elem, ok := p.index[fid]
	if !ok {
		return
	}
	p.ll.Remove(elem)
	delete(p.index, fid)
}

func (p *FIFOValueLogOffloadPolicy) DecideOffload(ctx ValueLogOffloadContext) []ValueLogOffloadDecision {
	closed := closedFids(ctx)
	keep := normalizeKeep(p.KeepLocalClosed)
	toEvict := len(closed) - keep
	if toEvict <= 0 {
		return nil
	}
	closedSet := make(map[uint32]struct{}, len(closed))
	for _, fid := range closed {
		closedSet[fid] = struct{}{}
	}
	ordered := make([]uint32, 0, len(closed))

	p.mu.Lock()
	p.ensure()
	// Seed unknown local fids deterministically (recovery/reopen path).
	// We use ascending fid as age proxy and append to the back so writable
	// (usually max fid) remains newest.
	unknownLocal := make([]uint32, 0)
	for _, fid := range ctx.LocalFids {
		if _, ok := p.index[fid]; !ok {
			unknownLocal = append(unknownLocal, fid)
		}
	}
	sort.Slice(unknownLocal, func(i, j int) bool { return unknownLocal[i] < unknownLocal[j] })
	for _, fid := range unknownLocal {
		p.index[fid] = p.ll.PushBack(fid)
	}

	for e := p.ll.Front(); e != nil; e = e.Next() {
		fid := e.Value.(uint32)
		if _, ok := closedSet[fid]; ok {
			ordered = append(ordered, fid)
			if len(ordered) == len(closed) {
				break
			}
		}
	}
	p.mu.Unlock()

	out := make([]ValueLogOffloadDecision, 0, toEvict)
	for _, fid := range ordered[:toEvict] {
		out = append(out, ValueLogOffloadDecision{Fid: fid, PruneLocal: p.PruneLocal})
	}
	return out
}

// LRUValueLogOffloadPolicy evicts least-recently-used closed local vlog files first.
type LRUValueLogOffloadPolicy struct {
	KeepLocalClosed int
	PruneLocal      bool

	mu    sync.Mutex
	ll    *clist.List
	index map[uint32]*clist.Element
}

func (p *LRUValueLogOffloadPolicy) ensure() {
	if p.ll == nil {
		p.ll = clist.New()
	}
	if p.index == nil {
		p.index = make(map[uint32]*clist.Element)
	}
}

func (p *LRUValueLogOffloadPolicy) OnLocalFileCreated(fid uint32) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ensure()
	if _, ok := p.index[fid]; ok {
		return
	}
	p.index[fid] = p.ll.PushBack(fid)
}

func (p *LRUValueLogOffloadPolicy) OnLocalFileRead(fid uint32) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ensure()
	elem, ok := p.index[fid]
	if !ok {
		elem = p.ll.PushBack(fid)
		p.index[fid] = elem
		return
	}
	p.ll.MoveToBack(elem)
}

func (p *LRUValueLogOffloadPolicy) OnLocalFileDeleted(fid uint32) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ensure()
	elem, ok := p.index[fid]
	if !ok {
		return
	}
	p.ll.Remove(elem)
	delete(p.index, fid)
}

func (p *LRUValueLogOffloadPolicy) DecideOffload(ctx ValueLogOffloadContext) []ValueLogOffloadDecision {
	closed := closedFids(ctx)
	if len(closed) == 0 {
		return nil
	}
	closedSet := make(map[uint32]struct{}, len(closed))
	for _, fid := range closed {
		closedSet[fid] = struct{}{}
	}
	ordered := make([]uint32, 0, len(closed))

	p.mu.Lock()
	p.ensure()
	for e := p.ll.Front(); e != nil; e = e.Next() {
		fid := e.Value.(uint32)
		if _, ok := closedSet[fid]; ok {
			ordered = append(ordered, fid)
			delete(closedSet, fid)
		}
	}
	// Unknown fids (e.g. recovered without events) are treated as coldest first.
	if len(closedSet) > 0 {
		missing := make([]uint32, 0, len(closedSet))
		for fid := range closedSet {
			missing = append(missing, fid)
			if _, ok := p.index[fid]; !ok {
				p.index[fid] = p.ll.PushFront(fid)
			}
		}
		sort.Slice(missing, func(i, j int) bool { return missing[i] < missing[j] })
		ordered = append(missing, ordered...)
	}
	p.mu.Unlock()

	return decisionsFromSortedFids(ordered, p.KeepLocalClosed, p.PruneLocal)
}

type lfuFidState struct {
	fid          uint32
	createdOrder uint64
	lastTouch    uint64
	accessCount  uint64
	index        int
}

// LFUValueLogOffloadPolicy evicts least-frequently-used closed local vlog files first.
type LFUValueLogOffloadPolicy struct {
	KeepLocalClosed int
	PruneLocal      bool

	mu    sync.Mutex
	seq   uint64
	tick  uint64
	state map[uint32]*lfuFidState
	hp    lfuHeap
}

func (p *LFUValueLogOffloadPolicy) ensure() {
	if p.state == nil {
		p.state = make(map[uint32]*lfuFidState)
	}
	if p.hp == nil {
		p.hp = make(lfuHeap, 0)
		heap.Init(&p.hp)
	}
}

type lfuHeap []*lfuFidState

func (h lfuHeap) Len() int { return len(h) }
func (h lfuHeap) Less(i, j int) bool {
	if h[i].accessCount != h[j].accessCount {
		return h[i].accessCount < h[j].accessCount
	}
	if h[i].lastTouch != h[j].lastTouch {
		return h[i].lastTouch < h[j].lastTouch
	}
	if h[i].createdOrder != h[j].createdOrder {
		return h[i].createdOrder < h[j].createdOrder
	}
	return h[i].fid < h[j].fid
}
func (h lfuHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}
func (h *lfuHeap) Push(x any) {
	n := len(*h)
	it := x.(*lfuFidState)
	it.index = n
	*h = append(*h, it)
}
func (h *lfuHeap) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	it.index = -1
	*h = old[:n-1]
	return it
}

func (p *LFUValueLogOffloadPolicy) OnLocalFileCreated(fid uint32) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ensure()
	if _, ok := p.state[fid]; ok {
		return
	}
	p.seq++
	p.tick++
	it := &lfuFidState{
		fid:          fid,
		createdOrder: p.seq,
		lastTouch:    p.tick,
	}
	p.state[fid] = it
	heap.Push(&p.hp, it)
}

func (p *LFUValueLogOffloadPolicy) OnLocalFileRead(fid uint32) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ensure()
	st, ok := p.state[fid]
	if !ok {
		p.seq++
		st = &lfuFidState{
			fid:          fid,
			createdOrder: p.seq,
			index:        -1,
		}
		p.state[fid] = st
		heap.Push(&p.hp, st)
	}
	p.tick++
	st.lastTouch = p.tick
	st.accessCount++
	if st.index >= 0 {
		heap.Fix(&p.hp, st.index)
	}
}

func (p *LFUValueLogOffloadPolicy) OnLocalFileDeleted(fid uint32) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ensure()
	st, ok := p.state[fid]
	if !ok {
		return
	}
	if st.index >= 0 {
		heap.Remove(&p.hp, st.index)
	}
	delete(p.state, fid)
}

func (p *LFUValueLogOffloadPolicy) DecideOffload(ctx ValueLogOffloadContext) []ValueLogOffloadDecision {
	closed := closedFids(ctx)
	keep := normalizeKeep(p.KeepLocalClosed)
	toEvict := len(closed) - keep
	if toEvict <= 0 {
		return nil
	}
	closedSet := make(map[uint32]struct{}, len(closed))
	for _, fid := range closed {
		closedSet[fid] = struct{}{}
	}
	selected := make([]uint32, 0, toEvict)
	popped := make([]*lfuFidState, 0, toEvict+1)

	p.mu.Lock()
	p.ensure()
	for len(selected) < toEvict && p.hp.Len() > 0 {
		it := heap.Pop(&p.hp).(*lfuFidState)
		popped = append(popped, it)
		if _, ok := closedSet[it.fid]; ok {
			selected = append(selected, it.fid)
			delete(closedSet, it.fid)
		}
	}
	for _, it := range popped {
		heap.Push(&p.hp, it)
	}
	// Unknown fids are treated as coldest.
	if len(selected) < toEvict && len(closedSet) > 0 {
		missing := make([]uint32, 0, len(closedSet))
		for fid := range closedSet {
			missing = append(missing, fid)
		}
		sort.Slice(missing, func(i, j int) bool { return missing[i] < missing[j] })
		for _, fid := range missing {
			selected = append(selected, fid)
			if len(selected) == toEvict {
				break
			}
		}
	}
	p.mu.Unlock()

	out := make([]ValueLogOffloadDecision, 0, len(selected))
	for _, fid := range selected {
		out = append(out, ValueLogOffloadDecision{Fid: fid, PruneLocal: p.PruneLocal})
	}
	return out
}
