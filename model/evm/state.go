package evm

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"sort"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/triedb"
	"github.com/holiman/uint256"
	model "github.com/lengzhao/GoChainKit/cap/model"
	"github.com/lengzhao/GoChainKit/core"
)

const (
	dbPrefix            = "model/evm/db/"
	rootKey             = "model/evm/root"
	identityKey         = "model/evm/identity"
	heightKey           = "model/evm/height"
	timeKey             = "model/evm/time"
	hashPrefix          = "model/evm/hash/"
	maxTransactionBytes = 1 << 20
	maxSnapshotBytes    = 64 << 20
)

type stateSession struct {
	db      ethdb.Database
	trie    *triedb.Database
	state   *state.StateDB
	initial map[string][]byte
}

func newStateSession(entries map[string][]byte) (*stateSession, error) {
	return newStateSessionWithLimit(entries, maxSnapshotBytes)
}

func newStateSessionWithLimit(entries map[string][]byte, limit int) (*stateSession, error) {
	db := rawdb.NewMemoryDatabase()
	size := 0
	for k, v := range entries {
		if !strings.HasPrefix(k, dbPrefix) {
			continue
		}
		// Match export's native key+value budget; the framework namespace is
		// bookkeeping and must not make an exported snapshot unrestorable.
		size += len(k) - len(dbPrefix) + len(v)
		if size > limit {
			db.Close()
			return nil, fmt.Errorf("evm: state budget exceeded")
		}
		if err := db.Put([]byte(strings.TrimPrefix(k, dbPrefix)), v); err != nil {
			db.Close()
			return nil, err
		}
	}
	root := types.EmptyRootHash
	if entries != nil {
		raw, ok := entries[rootKey]
		if !ok || len(raw) != 32 {
			db.Close()
			return nil, fmt.Errorf("evm: missing or corrupt state root")
		}
		root = common.BytesToHash(raw)
	}
	trie := triedb.NewDatabase(db, triedb.HashDefaults)
	st, err := state.New(root, state.NewMPTDatabase(trie, state.NewCodeDB(db)))
	if err != nil {
		trie.Close()
		db.Close()
		return nil, err
	}
	return &stateSession{db: db, trie: trie, state: st, initial: entries}, nil
}

func (m *stateSession) close() { _ = m.trie.Close(); _ = m.db.Close() }

func (m *stateSession) commit(ctx context.Context, rules params.Rules, height uint64) (map[string][]byte, error) {
	return m.commitWithLimit(ctx, rules, height, maxSnapshotBytes)
}

func (m *stateSession) commitWithLimit(ctx context.Context, rules params.Rules, height uint64, limit int) (map[string][]byte, error) {
	root, err := m.state.Commit(rules, height)
	if err != nil {
		return nil, err
	}
	if root != types.EmptyRootHash {
		if err = m.trie.Commit(root, false); err != nil {
			return nil, err
		}
	}
	out := map[string][]byte{rootKey: bytes.Clone(root[:])}
	it := m.db.NewIterator(nil, nil)
	defer it.Release()
	size := 0
	for it.Next() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		k, v := it.Key(), it.Value()
		size += len(k) + len(v)
		if size > limit {
			return nil, fmt.Errorf("evm: state budget exceeded")
		}
		out[dbPrefix+string(k)] = bytes.Clone(v)
	}
	return out, it.Error()
}

// changes compares the session's original snapshot with its finalized image.
// Only this model's namespace can be removed; unrelated parent state is borrowed.
func (m *stateSession) changes(next map[string][]byte) model.Transition {
	tr := model.Transition{Writes: make(map[string][]byte)}
	for k, v := range next {
		if !bytes.Equal(m.initial[k], v) {
			tr.Writes[k] = v
		}
	}
	for k := range m.initial {
		if strings.HasPrefix(k, "model/evm/") {
			if _, exists := next[k]; !exists {
				tr.Deletes = append(tr.Deletes, k)
			}
		}
	}
	sort.Strings(tr.Deletes)
	return tr
}

func (e *Engine) Genesis(ctx context.Context) (map[string][]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m, err := newStateSession(nil)
	if err != nil {
		return nil, err
	}
	defer m.close()
	addresses := make([]string, 0, len(e.cfg.Allocations))
	for address := range e.cfg.Allocations {
		addresses = append(addresses, address)
	}
	sort.Strings(addresses)
	for _, address := range addresses {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		a := e.cfg.Allocations[address]
		addr := common.HexToAddress(address)
		balance, _ := decimal(a.Balance)
		amount, _ := uint256.FromBig(balance)
		m.state.SetBalance(addr, amount, tracing.BalanceIncreaseGenesisBalance)
		m.state.SetNonce(addr, a.Nonce, tracing.NonceChangeGenesis)
		if len(a.Code) > 0 {
			m.state.SetCode(addr, a.Code, tracing.CodeChangeGenesis)
		}
		keys := make([]string, 0, len(a.Storage))
		for k := range a.Storage {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			m.state.SetState(addr, common.HexToHash(k), common.HexToHash(a.Storage[k]))
		}
	}
	// Match geth's genesis flush: empty-account pruning is a transaction rule,
	// not a reason to discard explicitly allocated genesis storage/accounts.
	out, err := m.commit(ctx, params.Rules{}, 0)
	if err != nil {
		return nil, err
	}
	out[identityKey] = bytes.Clone(e.identity)
	out[heightKey] = binary.BigEndian.AppendUint64(nil, 0)
	out[timeKey] = binary.BigEndian.AppendUint64(nil, 0)
	return out, nil
}

func (e *Engine) restore(ctx context.Context, parent core.StateReader) (*stateSession, error) {
	entries, err := parent.Entries(ctx)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(entries[identityKey], e.identity) || len(entries[heightKey]) != 8 || len(entries[timeKey]) != 8 {
		return nil, fmt.Errorf("evm: foreign or corrupt state metadata")
	}
	return newStateSession(entries)
}
