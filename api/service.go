// Package api provides transport-independent transaction admission and anchored
// chain queries. It owns no listeners, wallet keys or background workers.
package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/lengzhao/GoChainKit/cap/query"
	"github.com/lengzhao/GoChainKit/core"
)

var (
	ErrInvalidInput = errors.New("invalid API input")
	ErrTooLarge     = errors.New("transaction exceeds admission limit")
)

// InputError labels a pure codec/input rejection while preserving its cause.
// DecodeTransaction and EncodeTransaction are deterministic pure codecs. Generic
// validator failures are not input errors, even when a joined branch is invalid.
type InputError struct{ Cause error }

func (e *InputError) Error() string        { return fmt.Sprintf("%s: %v", ErrInvalidInput, e.Cause) }
func (e *InputError) Unwrap() error        { return e.Cause }
func (e *InputError) Is(target error) bool { return target == ErrInvalidInput }

// onlyInvalidTransaction classifies every leaf, so joins cannot hide local
// failures behind one recognizable rejection. Unknown leaves remain faults.
func onlyInvalidTransaction(err error) bool {
	if err == nil {
		return false
	}
	if many, ok := err.(interface{ Unwrap() []error }); ok {
		children := many.Unwrap()
		if len(children) == 0 {
			return false
		}
		for _, child := range children {
			if !onlyInvalidTransaction(child) {
				return false
			}
		}
		return true
	}
	if one, ok := err.(interface{ Unwrap() error }); ok {
		return onlyInvalidTransaction(one.Unwrap())
	}
	return err == core.ErrInvalidTransaction
}

type Config struct {
	MaxTransactionBytes int `json:"maxTransactionBytes"`
}

// Protocol is the minimal codec and admission validation dependency.
type Protocol interface {
	core.TransactionProtocol
	core.BlockCodec
}

// Ledger is one read-only owner, not separately wired canonical indices. This
// prevents compatible protocols from accidentally composing different ledgers.
type Ledger interface {
	core.StateStore
	query.ConfirmationReader
	Head(context.Context) (core.ID, error)
	Block(context.Context, core.ID) (core.Block, error)
}
type Deps struct {
	Protocol Protocol     `json:"protocol"`
	Ledger   Ledger       `json:"ledger"`
	Pool     core.Mempool `json:"pool"`
}
type Service struct {
	cfg    Config
	p      Protocol
	ledger Ledger
	pool   core.Mempool
}

func New(c Config, d Deps) (*Service, error) {
	if d.Protocol == nil || d.Ledger == nil || d.Pool == nil {
		return nil, fmt.Errorf("API protocol, ledger and pool required")
	}
	if err := core.RequireCompatible(d.Protocol.Protocol(), d.Ledger, d.Pool); err != nil {
		return nil, err
	}
	if d.Ledger.LedgerID() == nil {
		return nil, core.ErrLedgerMismatch
	}
	if c.MaxTransactionBytes == 0 {
		c.MaxTransactionBytes = 1536 * 1024
	}
	if c.MaxTransactionBytes < 1 {
		return nil, fmt.Errorf("positive transaction byte limit required")
	}
	return &Service{cfg: c, p: d.Protocol, ledger: d.Ledger, pool: d.Pool}, nil
}
func (s *Service) Protocol() core.ProtocolDescriptor { return s.p.Protocol() }
func (s *Service) LedgerID() *core.LedgerIdentity    { return s.ledger.LedgerID() }
func (s *Service) MaxTransactionBytes() int          { return s.cfg.MaxTransactionBytes }

// Submit freezes a caller object through canonical protocol encoding and revalidation.
func (s *Service) Submit(ctx context.Context, t core.Transaction) (query.Admission, error) {
	if err := ctx.Err(); err != nil {
		return query.Admission{}, err
	}
	if t == nil || t.ID() == "" {
		return query.Admission{}, ErrInvalidInput
	}
	data, err := s.p.EncodeTransaction(t)
	if err != nil {
		return query.Admission{}, &InputError{Cause: err}
	}
	owned, err := s.decode(ctx, data)
	if err != nil {
		return query.Admission{}, err
	}
	if owned.ID() != t.ID() {
		return query.Admission{}, fmt.Errorf("transaction codec changed identity")
	}
	return s.admit(ctx, owned)
}
func (s *Service) SubmitEncoded(ctx context.Context, data []byte) (query.Admission, error) {
	t, err := s.decode(ctx, data)
	if err != nil {
		return query.Admission{}, err
	}
	return s.admit(ctx, t)
}
func (s *Service) decode(ctx context.Context, data []byte) (core.Transaction, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(data) > s.cfg.MaxTransactionBytes {
		return nil, ErrTooLarge
	}
	t, err := s.p.DecodeTransaction(bytes.Clone(data))
	if err != nil {
		return nil, &InputError{Cause: err}
	}
	if t == nil || t.ID() == "" {
		return nil, ErrInvalidInput
	}
	if err = s.p.ValidateTransaction(ctx, t); err != nil {
		if onlyInvalidTransaction(err) {
			return nil, &InputError{Cause: err}
		}
		return nil, err
	}
	return t, nil
}
func (s *Service) admit(ctx context.Context, t core.Transaction) (query.Admission, error) {
	c, err := s.ledger.Confirmation(ctx, t.ID())
	if err == nil {
		if c.Transaction.Transaction == nil || c.Transaction.Transaction.ID() != t.ID() || c.Transaction.BlockID == "" || c.Head == "" {
			return query.Admission{}, fmt.Errorf("invalid confirmation view")
		}
		return query.Admission{TransactionID: t.ID(), Status: query.Confirmed, BlockID: c.Transaction.BlockID, Head: c.Head}, nil
	}
	if err != core.ErrNotFound {
		return query.Admission{}, err
	}
	if err = s.pool.Add(ctx, t); err != nil {
		return query.Admission{}, err
	}
	return query.Admission{TransactionID: t.ID(), Status: query.Accepted}, nil
}

type BlockView struct {
	ID   core.ID
	Data []byte
}
type HeadView struct {
	Block     BlockView
	StateRoot core.Commitment
}
type TransactionView struct {
	ID      core.ID
	Data    []byte
	Status  query.AdmissionStatus
	BlockID core.ID
	Index   uint64
	Head    core.ID
}
type ReceiptView struct {
	TransactionID core.ID
	BlockID       core.ID
	Index         uint64
	Head          core.ID
	Receipt       *core.Receipt
}

func (s *Service) Block(ctx context.Context, id core.ID) (BlockView, error) {
	b, err := s.ledger.Block(ctx, id)
	if err != nil {
		return BlockView{}, err
	}
	if b == nil || b.ID() != id {
		return BlockView{}, fmt.Errorf("block identity mismatch")
	}
	data, err := s.p.EncodeBlock(b)
	if err != nil {
		return BlockView{}, err
	}
	return BlockView{ID: id, Data: bytes.Clone(data)}, nil
}
func (s *Service) Head(ctx context.Context) (out HeadView, err error) {
	id, err := s.ledger.Head(ctx)
	if err != nil {
		return out, err
	}
	out.Block, err = s.Block(ctx, id)
	if err != nil {
		return out, err
	}
	snap, err := s.ledger.Snapshot(ctx, id)
	if err != nil {
		return out, err
	}
	out.StateRoot = snap.Root()
	err = snap.Close()
	return out, err
}
func (s *Service) Transaction(ctx context.Context, id core.ID) (TransactionView, error) {
	c, err := s.ledger.Confirmation(ctx, id)
	if err == nil {
		if c.Transaction.Transaction == nil || c.Transaction.Transaction.ID() != id {
			return TransactionView{}, fmt.Errorf("transaction identity mismatch")
		}
		data, e := s.p.EncodeTransaction(c.Transaction.Transaction)
		if e != nil {
			return TransactionView{}, e
		}
		return TransactionView{ID: id, Data: bytes.Clone(data), Status: query.Confirmed, BlockID: c.Transaction.BlockID, Index: c.Transaction.Index, Head: c.Head}, nil
	}
	if err != core.ErrNotFound {
		return TransactionView{}, err
	}
	candidates, err := s.pool.Candidates(ctx)
	if err != nil {
		return TransactionView{}, err
	}
	for _, t := range candidates {
		if err := ctx.Err(); err != nil {
			return TransactionView{}, err
		}
		if t != nil && t.ID() == id {
			data, e := s.p.EncodeTransaction(t)
			return TransactionView{ID: id, Data: bytes.Clone(data), Status: query.Accepted}, e
		}
	}
	return TransactionView{}, core.ErrNotFound
}

// Receipt returns a confirmed view even if this protocol omits receipts.
func (s *Service) Receipt(ctx context.Context, id core.ID) (ReceiptView, error) {
	c, err := s.ledger.Confirmation(ctx, id)
	if err != nil {
		return ReceiptView{}, err
	}
	out := ReceiptView{TransactionID: id, BlockID: c.Transaction.BlockID, Index: c.Transaction.Index, Head: c.Head}
	if c.Receipt != nil {
		r := *c.Receipt
		r.Data = bytes.Clone(r.Data)
		out.Receipt = &r
	}
	return out, nil
}

// Snapshot's reader is caller-owned and must be closed. Empty at captures Head
// once; subsequent canonical changes cannot alter the selected state.
func (s *Service) Snapshot(ctx context.Context, at core.ID) (core.ID, core.StateReader, error) {
	var err error
	if at == "" {
		at, err = s.ledger.Head(ctx)
		if err != nil {
			return "", nil, err
		}
	}
	snap, err := s.ledger.Snapshot(ctx, at)
	if err != nil {
		return "", nil, err
	}
	return at, snap, nil
}

var _ query.Submitter = (*Service)(nil)
