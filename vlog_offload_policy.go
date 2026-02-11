/*
 * SPDX-FileCopyrightText: © 2017-2025 Istari Digital, Inc.
 * SPDX-License-Identifier: Apache-2.0
 */

package badger

import (
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
	seq   uint64
	state map[uint32]uint64 // fid -> creation order
}

func (p *FIFOValueLogOffloadPolicy) ensure() {
	if p.state == nil {
		p.state = make(map[uint32]uint64)
	}
}

func (p *FIFOValueLogOffloadPolicy) OnLocalFileCreated(fid uint32) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ensure()
	if _, ok := p.state[fid]; ok {
		return
	}
	p.seq++
	p.state[fid] = p.seq
}

func (p *FIFOValueLogOffloadPolicy) OnLocalFileRead(_ uint32) {}

func (p *FIFOValueLogOffloadPolicy) OnLocalFileDeleted(fid uint32) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ensure()
	delete(p.state, fid)
}

func (p *FIFOValueLogOffloadPolicy) DecideOffload(ctx ValueLogOffloadContext) []ValueLogOffloadDecision {
	closed := closedFids(ctx)
	type cand struct {
		fid   uint32
		order uint64
	}
	cands := make([]cand, 0, len(closed))

	p.mu.Lock()
	p.ensure()
	for _, fid := range closed {
		order, ok := p.state[fid]
		if !ok {
			p.seq++
			order = p.seq
			p.state[fid] = order
		}
		cands = append(cands, cand{fid: fid, order: order})
	}
	p.mu.Unlock()

	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].order != cands[j].order {
			return cands[i].order < cands[j].order
		}
		return cands[i].fid < cands[j].fid
	})
	sorted := make([]uint32, 0, len(cands))
	for _, c := range cands {
		sorted = append(sorted, c.fid)
	}
	return decisionsFromSortedFids(sorted, p.KeepLocalClosed, p.PruneLocal)
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
	createdOrder uint64
	lastTouch    uint64
	accessCount  uint64
}

// LFUValueLogOffloadPolicy evicts least-frequently-used closed local vlog files first.
type LFUValueLogOffloadPolicy struct {
	KeepLocalClosed int
	PruneLocal      bool

	mu    sync.Mutex
	seq   uint64
	tick  uint64
	state map[uint32]lfuFidState
}

func (p *LFUValueLogOffloadPolicy) ensure() {
	if p.state == nil {
		p.state = make(map[uint32]lfuFidState)
	}
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
	p.state[fid] = lfuFidState{
		createdOrder: p.seq,
		lastTouch:    p.tick,
	}
}

func (p *LFUValueLogOffloadPolicy) OnLocalFileRead(fid uint32) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ensure()
	st, ok := p.state[fid]
	if !ok {
		p.seq++
		st.createdOrder = p.seq
	}
	p.tick++
	st.lastTouch = p.tick
	st.accessCount++
	p.state[fid] = st
}

func (p *LFUValueLogOffloadPolicy) OnLocalFileDeleted(fid uint32) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ensure()
	delete(p.state, fid)
}

func (p *LFUValueLogOffloadPolicy) DecideOffload(ctx ValueLogOffloadContext) []ValueLogOffloadDecision {
	closed := closedFids(ctx)
	type cand struct {
		fid          uint32
		accessCount  uint64
		lastTouch    uint64
		createdOrder uint64
	}
	cands := make([]cand, 0, len(closed))

	p.mu.Lock()
	p.ensure()
	for _, fid := range closed {
		st, ok := p.state[fid]
		if !ok {
			p.seq++
			st = lfuFidState{createdOrder: p.seq}
			p.state[fid] = st
		}
		cands = append(cands, cand{
			fid:          fid,
			accessCount:  st.accessCount,
			lastTouch:    st.lastTouch,
			createdOrder: st.createdOrder,
		})
	}
	p.mu.Unlock()

	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].accessCount != cands[j].accessCount {
			return cands[i].accessCount < cands[j].accessCount
		}
		if cands[i].lastTouch != cands[j].lastTouch {
			return cands[i].lastTouch < cands[j].lastTouch
		}
		if cands[i].createdOrder != cands[j].createdOrder {
			return cands[i].createdOrder < cands[j].createdOrder
		}
		return cands[i].fid < cands[j].fid
	})
	sorted := make([]uint32, 0, len(cands))
	for _, c := range cands {
		sorted = append(sorted, c.fid)
	}
	return decisionsFromSortedFids(sorted, p.KeepLocalClosed, p.PruneLocal)
}
