// Package powkv provides the development proof-of-work key-value protocol.
package powkv

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"github.com/lengzhao/GoChainKit/core"
	kv "github.com/lengzhao/GoChainKit/protocol/kv"
	"io"
	"math"
	"math/big"
)

type Config struct {
	ChainID string `json:"chainID"`
}
type protocol struct{ network string }
type Hash = kv.Hash
type Transaction = kv.Transaction
type Receipt = kv.Receipt
type BlockHeader = kv.BlockHeader
type Block struct {
	Header       BlockHeader
	Transactions []Transaction
	Nonce        uint64
}

const MaxChainIDBytes = kv.MaxChainIDBytes

var TransactionRoot = kv.TransactionRoot
var ReceiptRoot = kv.ReceiptRoot
var StateRoot = kv.StateRoot
var WrapTransaction = kv.WrapTransaction
var UnwrapTransaction = kv.UnwrapTransaction

type block struct {
	value Block
	id    core.ID
}

func (b *block) ID() core.ID { return b.id }
func (b Block) Clone() Block {
	v := (kv.Block{Header: b.Header, Transactions: b.Transactions}).Clone()
	b.Transactions = v.Transactions
	return b
}
func (b Block) ID() (Hash, error) {
	h, e := (kv.Block{Header: b.Header, Transactions: b.Transactions}).ID()
	if e != nil {
		return Hash{}, e
	}
	data := append([]byte("gochainkit/powkv/block/v1\x00"), h[:]...)
	data = binary.BigEndian.AppendUint64(data, b.Nonce)
	return sha256.Sum256(data), nil
}
func WrapBlock(b Block) (core.Block, error) {
	id, e := b.ID()
	if e != nil {
		return nil, e
	}
	for _, t := range b.Transactions {
		if e = t.Verify(b.Header.ChainID); e != nil {
			return nil, e
		}
	}
	return &block{b.Clone(), core.ID(string(id[:]))}, nil
}
func UnwrapBlock(b core.Block) (Block, error) {
	v, ok := b.(*block)
	if !ok || v == nil {
		return Block{}, fmt.Errorf("not a powkv block")
	}
	return v.value.Clone(), nil
}
func New(c Config) (core.WorkProtocol, error) {
	if c.ChainID == "" || len(c.ChainID) > MaxChainIDBytes {
		return nil, fmt.Errorf("invalid chainID")
	}
	return &protocol{c.ChainID}, nil
}
func (p *protocol) Protocol() core.ProtocolDescriptor {
	return core.ProtocolDescriptor{Name: "powkv", Version: "1", Network: p.network}
}
func strict(data []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		return e
	}
	var tail any
	if e := d.Decode(&tail); e != io.EOF {
		return fmt.Errorf("trailing JSON")
	}
	return nil
}
func (p *protocol) EncodeTransaction(t core.Transaction) ([]byte, error) {
	v, e := UnwrapTransaction(t)
	if e != nil {
		return nil, e
	}
	if e = v.Verify(p.network); e != nil {
		return nil, e
	}
	return json.Marshal(v)
}
func (p *protocol) DecodeTransaction(data []byte) (core.Transaction, error) {
	var t Transaction
	if e := strict(data, &t); e != nil {
		return nil, e
	}
	if e := t.Verify(p.network); e != nil {
		return nil, e
	}
	canonical, err := json.Marshal(t)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(canonical, data) {
		return nil, fmt.Errorf("noncanonical transaction encoding")
	}
	return WrapTransaction(t)
}
func (p *protocol) ValidateTransaction(ctx context.Context, t core.Transaction) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	v, e := UnwrapTransaction(t)
	if e != nil {
		return e
	}
	return v.Verify(p.network)
}
func (p *protocol) EncodeBlock(b core.Block) ([]byte, error) {
	v, e := UnwrapBlock(b)
	if e != nil {
		return nil, e
	}
	if v.Header.ChainID != p.network {
		return nil, fmt.Errorf("wrong chain")
	}
	return json.Marshal(v)
}
func (p *protocol) DecodeBlock(data []byte) (core.Block, error) {
	var b Block
	if e := strict(data, &b); e != nil {
		return nil, e
	}
	if b.Header.ChainID != p.network {
		return nil, fmt.Errorf("wrong chain")
	}
	canonical, err := json.Marshal(b)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(canonical, data) {
		return nil, fmt.Errorf("noncanonical block encoding")
	}
	return WrapBlock(b)
}
func (p *protocol) Transactions(b core.Block) ([]core.Transaction, error) {
	v, e := UnwrapBlock(b)
	if e != nil {
		return nil, e
	}
	out := make([]core.Transaction, len(v.Transactions))
	for i, t := range v.Transactions {
		out[i], e = WrapTransaction(t)
		if e != nil {
			return nil, e
		}
	}
	return out, nil
}
func (p *protocol) StateCommitment(ctx context.Context, s map[string][]byte) (core.Commitment, error) {
	if e := ctx.Err(); e != nil {
		return "", e
	}
	h := StateRoot(s)
	return core.Commitment(string(h[:])), nil
}
func (p *protocol) Genesis(ctx context.Context) (core.Block, map[string][]byte, error) {
	if e := ctx.Err(); e != nil {
		return nil, nil, e
	}
	s := map[string][]byte{}
	r, _ := TransactionRoot(nil)
	b, e := WrapBlock(Block{Header: BlockHeader{Version: 1, ChainID: p.network, TransactionRoot: r, StateRoot: StateRoot(s), ReceiptRoot: ReceiptRoot(nil)}})
	return b, s, e
}
func (p *protocol) Draft(ctx context.Context, parent core.Block, txs []core.Transaction) (core.Block, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	v, e := UnwrapBlock(parent)
	if e != nil {
		return nil, e
	}
	if v.Header.ChainID != p.network || v.Header.Height == math.MaxUint64 {
		return nil, fmt.Errorf("invalid parent")
	}
	id, _ := v.ID()
	b := Block{Header: BlockHeader{Version: 1, ChainID: p.network, Height: v.Header.Height + 1, Parent: id}}
	for _, t := range txs {
		if e = p.ValidateTransaction(ctx, t); e != nil {
			return nil, e
		}
		x, _ := UnwrapTransaction(t)
		b.Transactions = append(b.Transactions, x)
	}
	b.Header.TransactionRoot, e = TransactionRoot(b.Transactions)
	if e != nil {
		return nil, e
	}
	return WrapBlock(b)
}
func receiptRoot(rs []core.Receipt) (Hash, error) {
	out := make([]Receipt, len(rs))
	for i, r := range rs {
		if len(r.TransactionID) != 32 || !bytes.Equal(r.Data, []byte{1}) {
			return Hash{}, fmt.Errorf("invalid kv receipt")
		}
		copy(out[i].TransactionID[:], []byte(r.TransactionID))
		out[i].Success = true
	}
	return ReceiptRoot(out), nil
}
func (p *protocol) Finalize(ctx context.Context, d core.Block, r core.ExecutionResult) (core.Block, error) {
	b, err := p.finalizeUnsealed(ctx, d, r)
	if err != nil {
		return nil, err
	}
	return p.Seal(ctx, b)
}
func (p *protocol) finalizeUnsealed(ctx context.Context, d core.Block, r core.ExecutionResult) (core.Block, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	v, e := UnwrapBlock(d)
	if e != nil {
		return nil, e
	}
	if v.Header.ChainID != p.network || r.Parent != core.ID(string(v.Header.Parent[:])) || r.BlockID != d.ID() || len(r.StateRoot) != 32 {
		return nil, fmt.Errorf("execution binding mismatch")
	}
	if len(r.Receipts) != len(v.Transactions) {
		return nil, fmt.Errorf("receipt count")
	}
	for i, t := range v.Transactions {
		id, _ := t.ID()
		if r.Receipts[i].TransactionID != core.ID(string(id[:])) {
			return nil, fmt.Errorf("receipt transaction mismatch")
		}
	}
	copy(v.Header.StateRoot[:], []byte(r.StateRoot))
	v.Header.ReceiptRoot, e = receiptRoot(r.Receipts)
	if e != nil {
		return nil, e
	}
	return WrapBlock(v)
}
func (p *protocol) ValidateBlock(ctx context.Context, _ core.ChainStore, parent, b core.Block, r core.ExecutionResult) error {
	pv, e := UnwrapBlock(parent)
	if e != nil {
		return e
	}
	bv, e := UnwrapBlock(b)
	if e != nil {
		return e
	}
	if pv.Header.Height == math.MaxUint64 || bv.Header.Height != pv.Header.Height+1 || bv.Header.Version != 1 || bv.Header.ChainID != p.network || pv.Header.ChainID != p.network || core.ID(string(bv.Header.Parent[:])) != parent.ID() {
		return fmt.Errorf("invalid ancestry")
	}
	transactions, e := p.Transactions(b)
	if e != nil {
		return e
	}
	draft, e := p.Draft(ctx, parent, transactions)
	if e != nil {
		return e
	}
	if r.BlockID != b.ID() {
		return fmt.Errorf("result block binding")
	}
	r.BlockID = draft.ID()
	final, e := p.finalizeUnsealed(ctx, draft, r)
	if e != nil {
		return e
	}
	fv, _ := UnwrapBlock(final)
	fv.Nonce = bv.Nonce
	final, e = WrapBlock(fv)
	if e != nil {
		return e
	}
	if _, e = p.Work(b); e != nil {
		return e
	}
	if final.ID() != b.ID() {
		return fmt.Errorf("commitment mismatch")
	}
	return nil
}

// ValidateCandidate checks the default chain cursor before executing transactions.
func (p *protocol) ValidateCandidate(ctx context.Context, _ core.ChainStore, parent, b core.Block) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	pv, err := UnwrapBlock(parent)
	if err != nil {
		return err
	}
	bv, err := UnwrapBlock(b)
	if err != nil {
		return err
	}
	if pv.Header.Version != 1 || bv.Header.Version != 1 || pv.Header.ChainID != p.network || bv.Header.ChainID != p.network {
		return fmt.Errorf("invalid candidate protocol")
	}
	if core.ID(string(bv.Header.Parent[:])) != parent.ID() {
		return core.ErrConflict
	}
	if pv.Header.Height == math.MaxUint64 || bv.Header.Height != pv.Header.Height+1 {
		return fmt.Errorf("invalid candidate height")
	}
	return nil
}

// Parent reports the protocol ancestry cursor.
func (p *protocol) Parent(b core.Block) (core.ID, error) {
	v, e := UnwrapBlock(b)
	if e != nil {
		return "", e
	}
	if v.Header.Height == 0 {
		if _, err := p.Work(b); err != nil {
			return "", err
		}
		return "", nil
	}
	return core.ID(string(v.Header.Parent[:])), nil
}

// Work assigns constant work to valid blocks. Genesis has zero work.
func (p *protocol) Work(b core.Block) (*big.Int, error) {
	v, e := UnwrapBlock(b)
	if e != nil {
		return nil, e
	}
	if v.Header.ChainID != p.network {
		return nil, fmt.Errorf("wrong chain")
	}
	if v.Header.Height == 0 {
		g, _, e := p.Genesis(context.Background())
		if e != nil {
			return nil, e
		}
		if b.ID() != g.ID() {
			return nil, fmt.Errorf("invalid genesis")
		}
		return new(big.Int), nil
	}
	if len(b.ID()) != 32 || b.ID()[0] != 0 {
		return nil, fmt.Errorf("invalid proof of work")
	}
	return big.NewInt(256), nil
}

// Seal searches an eight-bit target. This is a development difficulty only.
func (p *protocol) Seal(ctx context.Context, b core.Block) (core.Block, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	v, e := UnwrapBlock(b)
	if e != nil {
		return nil, e
	}
	if v.Header.ChainID != p.network {
		return nil, fmt.Errorf("wrong chain")
	}
	if _, e = p.Work(b); e == nil {
		return b, nil
	}
	if v.Header.Height == 0 {
		return nil, fmt.Errorf("invalid genesis")
	}
	var seed [8]byte
	if _, err := rand.Read(seed[:]); err != nil {
		return nil, err
	}
	v.Nonce = binary.BigEndian.Uint64(seed[:])
	// Transaction/body commitments are invariant during nonce search. Hash the
	// validated header once rather than rehash and verify a maximal transaction
	// for every nonce attempt.
	base, e := (kv.Block{Header: v.Header, Transactions: v.Transactions}).ID()
	if e != nil {
		return nil, e
	}
	prefix := append([]byte("gochainkit/powkv/block/v1\x00"), base[:]...)
	for {
		if e = ctx.Err(); e != nil {
			return nil, e
		}
		data := binary.BigEndian.AppendUint64(prefix[:len(prefix):len(prefix)], v.Nonce)
		candidate := sha256.Sum256(data)
		if candidate[0] == 0 {
			return WrapBlock(v)
		}
		if v.Nonce == math.MaxUint64 {
			return nil, fmt.Errorf("nonce exhausted")
		}
		v.Nonce++
	}
}
