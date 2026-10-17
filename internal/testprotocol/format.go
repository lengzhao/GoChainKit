// Package testprotocol supplies a deliberately different format for contract tests.
// It has no signatures, nonce field, SHA-256 IDs or default block header.
package testprotocol

import (
	"bytes"
	"context"
	"crypto/sha512"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"github.com/lengzhao/GoChainKit/core"
	"io"
	"sort"
)

type Tx struct {
	Label string
	Body  []byte
}

func (t Tx) ID() core.ID {
	data, _ := json.Marshal(t)
	h := sha512.Sum512(data)
	return core.ID("tx/" + string(h[:]))
}

type Frame struct {
	Ancestor   core.ID
	Revision   uint64
	Records    []Tx
	Commitment core.Commitment
	OtherLinks []string
}

func (b Frame) ID() core.ID {
	data, _ := encodeFrame(b)
	h := sha512.Sum512(data)
	return core.ID("frame/" + string(h[:]))
}

type wireFrame struct {
	Ancestor   []byte
	Revision   uint64
	Records    []Tx
	Commitment []byte
	OtherLinks []string
}

func encodeFrame(b Frame) ([]byte, error) {
	return json.Marshal(wireFrame{Ancestor: []byte(b.Ancestor), Revision: b.Revision, Records: b.Records, Commitment: []byte(b.Commitment), OtherLinks: b.OtherLinks})
}

type Format struct{ Network string }

func New(network string) *Format { return &Format{network} }
func (p *Format) Protocol() core.ProtocolDescriptor {
	return core.ProtocolDescriptor{Name: "test-flex", Version: "7", Network: p.Network}
}
func strict(data []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return fmt.Errorf("trailing data")
	}
	return nil
}
func (p *Format) EncodeTransaction(t core.Transaction) ([]byte, error) {
	v, ok := t.(Tx)
	if !ok {
		return nil, fmt.Errorf("not a flex transaction")
	}
	return json.Marshal(v)
}
func (p *Format) DecodeTransaction(data []byte) (core.Transaction, error) {
	var t Tx
	err := strict(data, &t)
	if err != nil {
		return nil, err
	}
	if err := p.ValidateTransaction(context.Background(), t); err != nil {
		return nil, err
	}
	return t, nil
}
func (p *Format) ValidateTransaction(ctx context.Context, t core.Transaction) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	v, ok := t.(Tx)
	if !ok || v.Label == "" || len(v.Label) > 256 || len(v.Body) > 4096 {
		return core.ErrInvalidTransaction
	}
	return nil
}
func (p *Format) EncodeBlock(b core.Block) ([]byte, error) {
	v, ok := b.(Frame)
	if !ok {
		return nil, fmt.Errorf("not a flex frame")
	}
	return encodeFrame(v)
}
func (p *Format) DecodeBlock(data []byte) (core.Block, error) {
	var b wireFrame
	err := strict(data, &b)
	return Frame{Ancestor: core.ID(string(b.Ancestor)), Revision: b.Revision, Records: b.Records, Commitment: core.Commitment(string(b.Commitment)), OtherLinks: b.OtherLinks}, err
}
func (p *Format) Transactions(b core.Block) ([]core.Transaction, error) {
	v, ok := b.(Frame)
	if !ok {
		return nil, fmt.Errorf("not a flex frame")
	}
	out := make([]core.Transaction, len(v.Records))
	for i, t := range v.Records {
		t.Body = bytes.Clone(t.Body)
		out[i] = t
	}
	return out, nil
}
func (p *Format) StateCommitment(ctx context.Context, state map[string][]byte) (core.Commitment, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var b bytes.Buffer
	b.WriteString("flex-state")
	keys := make([]string, 0, len(state))
	for k := range state {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		_ = binary.Write(&b, binary.BigEndian, uint64(len(k)))
		b.WriteString(k)
		_ = binary.Write(&b, binary.BigEndian, uint64(len(state[k])))
		b.Write(state[k])
	}
	h := sha512.Sum512(b.Bytes())
	return core.Commitment(string(h[:])), nil
}
func (p *Format) Genesis(ctx context.Context) (core.Block, map[string][]byte, error) {
	root, err := p.StateCommitment(ctx, nil)
	return Frame{Commitment: root}, map[string][]byte{}, err
}
func (p *Format) Draft(ctx context.Context, parent core.Block, txs []core.Transaction) (core.Block, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	old, ok := parent.(Frame)
	if !ok {
		return nil, fmt.Errorf("not a flex parent")
	}
	b := Frame{Ancestor: parent.ID(), Revision: old.Revision + 1, OtherLinks: []string{"branch-alpha", "branch-beta"}}
	for _, t := range txs {
		if err := p.ValidateTransaction(ctx, t); err != nil {
			return nil, err
		}
		v := t.(Tx)
		v.Body = bytes.Clone(v.Body)
		b.Records = append(b.Records, v)
	}
	return b, nil
}
func (p *Format) Finalize(ctx context.Context, d core.Block, r core.ExecutionResult) (core.Block, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	b, ok := d.(Frame)
	if !ok {
		return nil, fmt.Errorf("not a flex frame")
	}
	b.Commitment = r.StateRoot
	return b, nil
}
func (p *Format) ValidateBlock(ctx context.Context, _ core.ChainStore, parent, b core.Block, r core.ExecutionResult) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	old, ok := parent.(Frame)
	if !ok {
		return fmt.Errorf("not a flex parent")
	}
	v, ok := b.(Frame)
	if !ok || v.Ancestor != parent.ID() || r.Parent != parent.ID() || v.Revision != old.Revision+1 || v.Commitment != r.StateRoot || r.BlockID != b.ID() {
		return core.ErrConflict
	}
	for _, t := range v.Records {
		if err := p.ValidateTransaction(ctx, t); err != nil {
			return err
		}
	}
	return nil
}

type Executor struct{ P core.Protocol }

func (e Executor) Protocol() core.ProtocolDescriptor { return e.P.Protocol() }
func (e Executor) Execute(ctx context.Context, s core.StateReader, b core.Block) (core.ExecutionResult, error) {
	v, ok := b.(Frame)
	if !ok {
		return core.ExecutionResult{}, fmt.Errorf("not flex")
	}
	state, err := s.Entries(ctx)
	if err != nil {
		return core.ExecutionResult{}, err
	}
	if state == nil {
		state = map[string][]byte{}
	}
	writes := map[string][]byte{}
	for _, t := range v.Records {
		if err := e.P.ValidateTransaction(ctx, t); err != nil {
			return core.ExecutionResult{}, err
		}
		state[t.Label] = bytes.Clone(t.Body)
		writes[t.Label] = bytes.Clone(t.Body)
	}
	root, err := e.P.StateCommitment(ctx, state)
	return core.ExecutionResult{Parent: v.Ancestor, BlockID: b.ID(), Writes: writes, StateRoot: root}, err
}
