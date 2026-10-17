package storage

import (
	"context"
	"fmt"
	"github.com/lengzhao/GoChainKit/core"
	db "go.etcd.io/bbolt"
	"math/big"
	"reflect"
)

type branchNode struct {
	Parent   core.ID
	Total    string
	Prepared preparedCommit
}
type branchPlan struct {
	Head               core.ID
	Detached, Attached []core.ID
	Transactions       map[core.ID]storedTransaction
	Receipts           map[core.ID]core.Receipt
}

func branchGenesis(ctx context.Context, p core.WorkProtocol, g preparedCommit) (branchNode, error) {
	b, e := decodeStoredBlock(p, g.Block, g.ID)
	if e != nil {
		return branchNode{}, e
	}
	parent, e := p.Parent(b)
	if e != nil {
		return branchNode{}, e
	}
	w, e := p.Work(b)
	if e != nil {
		return branchNode{}, e
	}
	if parent != "" || w == nil || w.Sign() != 0 {
		return branchNode{}, fmt.Errorf("genesis must have no parent and zero work")
	}
	return branchNode{Total: "0", Prepared: g}, nil
}
func branchCandidate(ctx context.Context, p core.WorkProtocol, chain core.LedgerReader, parent core.ID, b core.Block, r core.ExecutionResult, graph map[core.ID]branchNode) (branchNode, error) {
	ancestor, ok := graph[parent]
	if !ok {
		return branchNode{}, core.ErrNotFound
	}
	link, e := p.Parent(b)
	if e != nil {
		return branchNode{}, e
	}
	if link != parent {
		return branchNode{}, core.ErrConflict
	}
	v, e := prepareCommit(ctx, p, chain, parent, b, r)
	if e != nil {
		return branchNode{}, e
	}
	path, e := branchPath(graph, parent)
	if e != nil {
		return branchNode{}, e
	}
	for _, ancestorID := range path {
		for tx := range v.Transactions {
			if _, exists := graph[ancestorID].Prepared.Transactions[tx]; exists {
				return branchNode{}, fmt.Errorf("transaction repeated in branch")
			}
		}
	}
	w, e := p.Work(b)
	if e != nil {
		return branchNode{}, e
	}
	if w == nil || w.Sign() <= 0 {
		return branchNode{}, fmt.Errorf("nonpositive block work")
	}
	total, ok := new(big.Int).SetString(ancestor.Total, 10)
	if !ok {
		return branchNode{}, fmt.Errorf("corrupt cumulative work")
	}
	total.Add(total, w)
	return branchNode{Parent: parent, Total: total.String(), Prepared: v}, nil
}
func branchPath(graph map[core.ID]branchNode, tip core.ID) ([]core.ID, error) {
	var path []core.ID
	seen := map[core.ID]bool{}
	for tip != "" {
		if seen[tip] {
			return nil, fmt.Errorf("branch cycle")
		}
		seen[tip] = true
		n, ok := graph[tip]
		if !ok {
			return nil, core.ErrNotFound
		}
		path = append(path, tip)
		tip = n.Parent
	}
	return path, nil
}
func planBranch(graph map[core.ID]branchNode, old, id core.ID) (branchPlan, error) {
	out := branchPlan{Head: old}
	oldWork, ok := new(big.Int).SetString(graph[old].Total, 10)
	if !ok {
		return out, fmt.Errorf("missing canonical work")
	}
	newWork, ok := new(big.Int).SetString(graph[id].Total, 10)
	if !ok {
		return out, fmt.Errorf("missing candidate work")
	}
	if newWork.Cmp(oldWork) <= 0 {
		return out, nil
	}
	newPath, e := branchPath(graph, id)
	if e != nil {
		return out, e
	}
	oldPath, e := branchPath(graph, old)
	if e != nil {
		return out, e
	}
	positions := map[core.ID]int{}
	for i, v := range newPath {
		positions[v] = i
	}
	found := false
	for i, v := range oldPath {
		if n, ok := positions[v]; ok {
			out.Detached = append(out.Detached, oldPath[:i]...)
			for j := n - 1; j >= 0; j-- {
				out.Attached = append(out.Attached, newPath[j])
			}
			found = true
			break
		}
	}
	if !found {
		return out, fmt.Errorf("disconnected genesis")
	}
	out.Head = id
	out.Transactions = map[core.ID]storedTransaction{}
	out.Receipts = map[core.ID]core.Receipt{}
	for i := len(newPath) - 1; i >= 0; i-- {
		n := graph[newPath[i]]
		for tx, r := range n.Prepared.Transactions {
			if _, ok := out.Transactions[tx]; ok {
				return out, fmt.Errorf("transaction repeated in branch")
			}
			out.Transactions[tx] = r
		}
		for tx, r := range n.Prepared.Receipts {
			out.Receipts[tx] = r
		}
	}
	return out, nil
}
func branchChange(p core.WorkProtocol, graph map[core.ID]branchNode, old core.ID, plan branchPlan) (core.BranchChange, error) {
	out := core.BranchChange{OldHead: old, NewHead: plan.Head}
	for _, id := range plan.Detached {
		b, e := decodeStoredBlock(p, graph[id].Prepared.Block, id)
		if e != nil {
			return out, e
		}
		out.Detached = append(out.Detached, b)
	}
	for _, id := range plan.Attached {
		b, e := decodeStoredBlock(p, graph[id].Prepared.Block, id)
		if e != nil {
			return out, e
		}
		out.Attached = append(out.Attached, b)
	}
	return out, nil
}
func (s *MemoryStore) Import(ctx context.Context, parent core.ID, b core.Block, r core.ExecutionResult) (core.BranchChange, error) {
	return s.importBlock(ctx, parent, b, r, false)
}
func (s *MemoryStore) importBlock(ctx context.Context, parent core.ID, b core.Block, r core.ExecutionResult, linear bool) (core.BranchChange, error) {
	p, ok := s.p.(core.WorkProtocol)
	if !ok {
		return core.BranchChange{}, fmt.Errorf("work protocol required")
	}
	if b == nil {
		return core.BranchChange{}, fmt.Errorf("nil block")
	}
	s.mu.RLock()
	head := s.head
	graph := make(map[core.ID]branchNode, len(s.branches)+1)
	for id, n := range s.branches {
		graph[id] = n
	}
	s.mu.RUnlock()
	if linear && parent != head {
		return core.BranchChange{}, core.ErrConflict
	}
	if _, ok := graph[b.ID()]; ok {
		return core.BranchChange{}, core.ErrAlreadyCommitted
	}
	n, e := branchCandidate(ctx, p, s, parent, b, r, graph)
	if e != nil {
		return core.BranchChange{}, e
	}
	graph[b.ID()] = n
	plan, e := planBranch(graph, head, b.ID())
	if e != nil {
		return core.BranchChange{}, e
	}
	change, e := branchChange(p, graph, head, plan)
	if e != nil {
		return change, e
	}
	id := n.Prepared.ID
	s.mu.Lock()
	defer s.mu.Unlock()
	if e = ctx.Err(); e != nil {
		return change, e
	}
	if _, ok := s.branches[id]; ok {
		return change, core.ErrAlreadyCommitted
	}
	if s.head != head {
		return change, core.ErrConflict
	}
	s.branches[id] = n
	s.blocks[id] = n.Prepared.Block
	s.states[id] = n.Prepared.State
	s.roots[id] = n.Prepared.Root
	if plan.Head != head {
		s.transactions = plan.Transactions
		s.receipts = plan.Receipts
		s.head = plan.Head
	}
	return change, nil
}
func (s *BoltStore) Import(ctx context.Context, parent core.ID, b core.Block, r core.ExecutionResult) (core.BranchChange, error) {
	return s.importBlock(ctx, parent, b, r, false)
}
func (s *BoltStore) importBlock(ctx context.Context, parent core.ID, b core.Block, r core.ExecutionResult, linear bool) (core.BranchChange, error) {
	p, ok := s.p.(core.WorkProtocol)
	if !ok {
		return core.BranchChange{}, fmt.Errorf("work protocol required")
	}
	if b == nil {
		return core.BranchChange{}, fmt.Errorf("nil block")
	}
	graph, head, e := s.branchGraph(ctx)
	if e != nil {
		return core.BranchChange{}, e
	}
	if linear && head != parent {
		return core.BranchChange{}, core.ErrConflict
	}
	if _, ok := graph[b.ID()]; ok {
		return core.BranchChange{}, core.ErrAlreadyCommitted
	}
	n, e := branchCandidate(ctx, p, s, parent, b, r, graph)
	if e != nil {
		return core.BranchChange{}, e
	}
	graph[b.ID()] = n
	plan, e := planBranch(graph, head, b.ID())
	if e != nil {
		return core.BranchChange{}, e
	}
	change, e := branchChange(p, graph, head, plan)
	if e != nil {
		return change, e
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return change, core.ErrClosed
	}
	var callbackErr error
	e = s.db.Update(func(tx *db.Tx) error { callbackErr = s.commitBranch(ctx, tx, head, n, plan); return callbackErr })
	if e != nil && callbackErr == nil {
		return change, fmt.Errorf("%w: %v", core.ErrCommitUnknown, e)
	}
	return change, e
}
func (s *BoltStore) branchGraph(ctx context.Context) (map[core.ID]branchNode, core.ID, error) {
	graph := map[core.ID]branchNode{}
	var head core.ID
	e := s.view(ctx, func(tx *db.Tx) error {
		if e := get(tx, "meta", []byte("head"), &head); e != nil {
			return e
		}
		bucket := tx.Bucket([]byte("branches"))
		if bucket == nil {
			return fmt.Errorf("missing branch records")
		}
		return bucket.ForEach(func(k, v []byte) error {
			var n branchNode
			if e := get(tx, "branches", k, &n); e != nil {
				return e
			}
			graph[core.ID(k)] = n
			return nil
		})
	})
	return graph, head, e
}
func (s *BoltStore) commitBranch(ctx context.Context, tx *db.Tx, expected core.ID, n branchNode, plan branchPlan) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	id := n.Prepared.ID
	if tx.Bucket([]byte("blocks")).Get([]byte(id)) != nil {
		return core.ErrAlreadyCommitted
	}
	var head core.ID
	if e := get(tx, "meta", []byte("head"), &head); e != nil {
		return e
	}
	if head != expected {
		return core.ErrConflict
	}
	if e := put(tx, "branches", []byte(id), n); e != nil {
		return e
	}
	for _, item := range []struct {
		b string
		v any
	}{{"blocks", n.Prepared.Block}, {"states", n.Prepared.State}, {"roots", n.Prepared.Root}, {"results", n.Prepared.Result}} {
		if e := put(tx, item.b, []byte(id), item.v); e != nil {
			return e
		}
	}
	if plan.Head != expected {
		for _, name := range []string{"transactions", "receipts"} {
			if e := tx.DeleteBucket([]byte(name)); e != nil {
				return e
			}
			if _, e := tx.CreateBucket([]byte(name)); e != nil {
				return e
			}
		}
		for id, r := range plan.Transactions {
			if e := put(tx, "transactions", []byte(id), r); e != nil {
				return e
			}
		}
		for id, r := range plan.Receipts {
			if e := put(tx, "receipts", []byte(id), r); e != nil {
				return e
			}
		}
		if e := put(tx, "meta", []byte("head"), plan.Head); e != nil {
			return e
		}
	}
	if s.beforeHead != nil {
		if e := s.beforeHead(); e != nil {
			return e
		}
	}
	return ctx.Err()
}

// validateBranches revalidates every stored fork before publishing the database.
func (s *BoltStore) validateBranches(ctx context.Context, p core.WorkProtocol, g preparedCommit) error {
	graph, head, e := s.branchGraph(ctx)
	if e != nil {
		return e
	}
	genesis, ok := graph[g.ID]
	if !ok || genesis.Parent != "" || genesis.Total != "0" || genesis.Prepared.ID != g.ID {
		return fmt.Errorf("invalid branch genesis")
	}
	if !reflect.DeepEqual(genesis.Prepared.Block, g.Block) || !reflect.DeepEqual(genesis.Prepared.State, g.State) || genesis.Prepared.Root != g.Root {
		return fmt.Errorf("branch genesis mismatch")
	}
	if _, ok := graph[head]; !ok {
		return fmt.Errorf("missing canonical branch")
	}
	visiting := map[core.ID]bool{}
	totals := map[core.ID]*big.Int{g.ID: big.NewInt(0)}
	var total func(core.ID) (*big.Int, error)
	total = func(id core.ID) (*big.Int, error) {
		if v, ok := totals[id]; ok {
			return new(big.Int).Set(v), nil
		}
		n, ok := graph[id]
		if !ok {
			return nil, core.ErrNotFound
		}
		if visiting[id] {
			return nil, fmt.Errorf("branch cycle")
		}
		visiting[id] = true
		parent, e := total(n.Parent)
		if e != nil {
			return nil, e
		}
		b, e := s.Block(ctx, id)
		if e != nil {
			return nil, e
		}
		link, e := p.Parent(b)
		if e != nil || link != n.Parent {
			return nil, fmt.Errorf("branch parent mismatch")
		}
		prepared, e := prepareCommit(ctx, p, s, n.Parent, b, n.Prepared.Result)
		if e != nil {
			return nil, e
		}
		if !reflect.DeepEqual(prepared, n.Prepared) {
			return nil, fmt.Errorf("branch records mismatch")
		}
		snap, e := s.Snapshot(ctx, id)
		if e != nil {
			return nil, e
		}
		entries, e := snap.Entries(ctx)
		root := snap.Root()
		snap.Close()
		if e != nil {
			return nil, e
		}
		if root != prepared.Root || !reflect.DeepEqual(entries, prepared.State) {
			return nil, fmt.Errorf("branch snapshot mismatch")
		}
		ancestorPath, e := branchPath(graph, n.Parent)
		if e != nil {
			return nil, e
		}
		for _, ancestorID := range ancestorPath {
			for tx := range prepared.Transactions {
				if _, exists := graph[ancestorID].Prepared.Transactions[tx]; exists {
					return nil, fmt.Errorf("transaction repeated in recovered branch")
				}
			}
		}
		w, e := p.Work(b)
		if e != nil {
			return nil, e
		}
		if w == nil || w.Sign() <= 0 {
			return nil, fmt.Errorf("invalid branch work")
		}
		parent.Add(parent, w)
		if n.Total != parent.String() {
			return nil, fmt.Errorf("cumulative work mismatch")
		}
		totals[id] = parent
		visiting[id] = false
		return new(big.Int).Set(parent), nil
	}
	for id := range graph {
		if _, e := total(id); e != nil {
			return e
		}
	}
	for id := range graph {
		if totals[id].Cmp(totals[head]) > 0 {
			return fmt.Errorf("canonical branch not heaviest")
		}
	}
	path, e := branchPath(graph, head)
	if e != nil {
		return e
	}
	expectedTx := map[core.ID]storedTransaction{}
	expectedReceipts := map[core.ID]core.Receipt{}
	for _, id := range path {
		for tx, r := range graph[id].Prepared.Transactions {
			if _, exists := expectedTx[tx]; exists {
				return fmt.Errorf("duplicate canonical transaction")
			}
			expectedTx[tx] = r
		}
		for tx, r := range graph[id].Prepared.Receipts {
			expectedReceipts[tx] = r
		}
	}
	return s.view(ctx, func(tx *db.Tx) error {
		count := 0
		if e := tx.Bucket([]byte("blocks")).ForEach(func(k, v []byte) error {
			if _, ok := graph[core.ID(k)]; !ok {
				return fmt.Errorf("block absent from branch graph")
			}
			count++
			return nil
		}); e != nil {
			return e
		}
		if count != len(graph) {
			return fmt.Errorf("branch graph block count mismatch")
		}
		actualTx := map[core.ID]storedTransaction{}
		if e := tx.Bucket([]byte("transactions")).ForEach(func(k, v []byte) error {
			var r storedTransaction
			if e := get(tx, "transactions", k, &r); e != nil {
				return e
			}
			actualTx[core.ID(k)] = r
			return nil
		}); e != nil {
			return e
		}
		actualReceipts := map[core.ID]core.Receipt{}
		if e := tx.Bucket([]byte("receipts")).ForEach(func(k, v []byte) error {
			var r core.Receipt
			if e := get(tx, "receipts", k, &r); e != nil {
				return e
			}
			actualReceipts[core.ID(k)] = r
			return nil
		}); e != nil {
			return e
		}
		if !reflect.DeepEqual(actualTx, expectedTx) || !reflect.DeepEqual(actualReceipts, expectedReceipts) {
			return fmt.Errorf("canonical branch index mismatch")
		}
		return nil
	})
}
