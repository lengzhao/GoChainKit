package storage

import (
	"bytes"
	"context"
	"fmt"
	"github.com/lengzhao/GoChainKit/cap/query"
	capstorage "github.com/lengzhao/GoChainKit/cap/storage"
	"github.com/lengzhao/GoChainKit/core"
)

// Confirmation snapshots canonical transaction, optional receipt and head under
// one read lock. Protocol callbacks run after releasing the backend lock.
func (s *MemoryStore) Confirmation(ctx context.Context, id core.ID) (query.Confirmation, error) {
	if err := ctx.Err(); err != nil {
		return query.Confirmation{}, err
	}
	s.mu.RLock()
	r, ok := s.transactions[id]
	r.Data = bytes.Clone(r.Data)
	receipt, hasReceipt := s.receipts[id]
	receipt.Data = bytes.Clone(receipt.Data)
	head := s.head
	s.mu.RUnlock()
	if !ok {
		return query.Confirmation{}, core.ErrNotFound
	}
	t, err := decodeStoredTransaction(s.p, r.Data, id)
	if err != nil {
		return query.Confirmation{}, fmt.Errorf("decode confirmed transaction: %w", err)
	}
	out := query.Confirmation{Transaction: core.ConfirmedTransaction{Transaction: t, BlockID: r.BlockID, Index: r.Index}, Head: head}
	if hasReceipt {
		out.Receipt = &receipt
	}
	return out, nil
}
func (s *KVStore) Confirmation(ctx context.Context, id core.ID) (query.Confirmation, error) {
	var record storedTransaction
	var receipt core.Receipt
	var head core.ID
	var hasReceipt bool
	err := s.view(ctx, func(tx capstorage.Reader) error {
		if err := get(tx, "transactions", []byte(id), &record); err != nil {
			return err
		}
		if err := get(tx, "meta", []byte("head"), &head); err != nil {
			return fmt.Errorf("confirmation head: %w", err)
		}
		err := get(tx, "receipts", []byte(id), &receipt)
		if err == nil {
			hasReceipt = true
			return nil
		}
		if err == core.ErrNotFound {
			return nil
		}
		return err
	})
	if err != nil {
		return query.Confirmation{}, err
	}
	t, err := decodeStoredTransaction(s.p, record.Data, id)
	if err != nil {
		return query.Confirmation{}, fmt.Errorf("decode confirmed transaction: %w", err)
	}
	out := query.Confirmation{Transaction: core.ConfirmedTransaction{Transaction: t, BlockID: record.BlockID, Index: record.Index}, Head: head}
	if hasReceipt {
		receipt.Data = bytes.Clone(receipt.Data)
		out.Receipt = &receipt
	}
	return out, nil
}

var _ query.ConfirmationReader = (*MemoryStore)(nil)
var _ query.ConfirmationReader = (*KVStore)(nil)
