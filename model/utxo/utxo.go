// Package utxo implements signed spend/create transitions without consensus rules.
package utxo

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	assetcap "github.com/lengzhao/GoChainKit/cap/asset"
	"github.com/lengzhao/GoChainKit/cap/model"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/GoChainKit/internal/jsoncodec"
	"github.com/lengzhao/GoChainKit/model/asset"
	"sort"
)

type Outpoint struct {
	TransactionID [32]byte
	Index         uint32
}
type Input struct {
	Previous             Outpoint
	PublicKey, Signature []byte
}
type Output struct {
	To     assetcap.Address
	Amount uint64
}
type Transaction struct {
	Network string
	Inputs  []Input
	Outputs []Output
}
type transaction struct {
	value Transaction
	id    core.ID
}

func (t *transaction) ID() core.ID { return t.id }
func (t Transaction) clone() Transaction {
	t.Inputs = append([]Input(nil), t.Inputs...)
	for i := range t.Inputs {
		t.Inputs[i].PublicKey = bytes.Clone(t.Inputs[i].PublicKey)
		t.Inputs[i].Signature = bytes.Clone(t.Inputs[i].Signature)
	}
	t.Outputs = append([]Output(nil), t.Outputs...)
	return t
}
func (t Transaction) payload() ([]byte, error) {
	if t.Network == "" || len(t.Network) > 128 || len(t.Inputs) == 0 || len(t.Inputs) > 128 || len(t.Outputs) == 0 || len(t.Outputs) > 128 {
		return nil, fmt.Errorf("invalid utxo transaction")
	}
	seen := map[Outpoint]bool{}
	var total uint64
	for _, o := range t.Outputs {
		if o.Amount == 0 {
			return nil, fmt.Errorf("zero output")
		}
		var err error
		total, err = asset.Add(total, o.Amount)
		if err != nil {
			return nil, err
		}
	}
	t = t.clone()
	for i := range t.Inputs {
		input := &t.Inputs[i]
		if seen[input.Previous] || len(input.PublicKey) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("invalid or duplicate input")
		}
		seen[input.Previous] = true
		input.Signature = nil
	}
	data, err := json.Marshal(t)
	return append([]byte("gochainkit/utxo/sign/v1\x00"), data...), err
}
func (t *Transaction) Sign(keys ...ed25519.PrivateKey) error {
	if t == nil || len(keys) != len(t.Inputs) {
		return fmt.Errorf("one key per input required")
	}
	for i, key := range keys {
		if len(key) != ed25519.PrivateKeySize || !bytes.Equal(ed25519.NewKeyFromSeed(key[:32]), key) {
			return fmt.Errorf("invalid key")
		}
		t.Inputs[i].PublicKey = bytes.Clone(key[32:])
	}
	payload, err := t.payload()
	if err != nil {
		return err
	}
	for i, key := range keys {
		t.Inputs[i].Signature = ed25519.Sign(key, payload)
	}
	return nil
}
func verify(t Transaction, network string) error {
	if t.Network != network {
		return fmt.Errorf("wrong network")
	}
	payload, err := t.payload()
	if err != nil {
		return err
	}
	for _, input := range t.Inputs {
		if len(input.Signature) != ed25519.SignatureSize || !ed25519.Verify(input.PublicKey, payload, input.Signature) {
			return fmt.Errorf("invalid input signature")
		}
	}
	return nil
}
func Wrap(t Transaction) (core.Transaction, error) {
	if err := verify(t, t.Network); err != nil {
		return nil, err
	}
	data, err := json.Marshal(t)
	if err != nil {
		return nil, err
	}
	id := sha256.Sum256(append([]byte("gochainkit/utxo/tx/v1\x00"), data...))
	return &transaction{t.clone(), core.ID(string(id[:]))}, nil
}
func Unwrap(tx core.Transaction) (Transaction, error) {
	t, ok := tx.(*transaction)
	if !ok || t == nil {
		return Transaction{}, fmt.Errorf("not a utxo transaction")
	}
	return t.value.clone(), nil
}
func Outpoints(tx core.Transaction) ([]Outpoint, error) {
	t, err := Unwrap(tx)
	if err != nil {
		return nil, err
	}
	var id [32]byte
	copy(id[:], []byte(tx.ID()))
	out := make([]Outpoint, len(t.Outputs))
	for i := range out {
		out[i] = Outpoint{TransactionID: id, Index: uint32(i)}
	}
	return out, nil
}

type Config struct {
	Network          string
	Allocations      []assetcap.Allocation
	CoinbaseMaturity uint64 `json:"coinbaseMaturity,omitempty"`
}
type Engine struct {
	network     string
	allocations []assetcap.Allocation
	genesisID   [32]byte
	maturity    uint64
}

func New(c Config) (*Engine, error) {
	if c.Network == "" || len(c.Network) > 128 || uint64(len(c.Allocations)) > uint64(^uint32(0)) {
		return nil, fmt.Errorf("invalid genesis")
	}
	var supply uint64
	for _, a := range c.Allocations {
		if a.Amount == 0 {
			return nil, fmt.Errorf("zero allocation")
		}
		var err error
		supply, err = asset.Add(supply, a.Amount)
		if err != nil {
			return nil, err
		}
	}
	data, _ := json.Marshal(c)
	id := sha256.Sum256(append([]byte("gochainkit/utxo/genesis/v1\x00"), data...))
	return &Engine{c.Network, append([]assetcap.Allocation(nil), c.Allocations...), id, c.CoinbaseMaturity}, nil
}
func (e *Engine) Protocol() core.ProtocolDescriptor {
	version := "1"
	if e.maturity != 0 {
		version = fmt.Sprintf("2/maturity/%d", e.maturity)
	}
	return core.ProtocolDescriptor{Name: "utxo", Version: version, Network: e.network}
}
func outputKey(o Outpoint) string {
	return "model/utxo/output/" + string(o.TransactionID[:]) + string(binary.BigEndian.AppendUint32(nil, o.Index))
}
func encode(o Output) []byte {
	return binary.BigEndian.AppendUint64(append([]byte(nil), o.To[:]...), o.Amount)
}
func decode(data []byte) (Output, error) {
	if len(data) != 40 {
		return Output{}, fmt.Errorf("corrupt output")
	}
	var o Output
	copy(o.To[:], data[:32])
	o.Amount = binary.BigEndian.Uint64(data[32:])
	if o.Amount == 0 {
		return Output{}, fmt.Errorf("corrupt zero output")
	}
	return o, nil
}
func Lookup(ctx context.Context, state core.StateReader, o Outpoint) (Output, error) {
	data, err := state.Get(ctx, []byte(outputKey(o)))
	if err != nil {
		return Output{}, err
	}
	return decode(data)
}
func (e *Engine) GenesisOutpoints() []Outpoint {
	out := make([]Outpoint, len(e.allocations))
	for i := range out {
		out[i] = Outpoint{TransactionID: e.genesisID, Index: uint32(i)}
	}
	return out
}
func (e *Engine) Genesis(ctx context.Context) (map[string][]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := map[string][]byte{}
	for i, a := range e.allocations {
		out[outputKey(Outpoint{e.genesisID, uint32(i)})] = encode(Output{a.Address, a.Amount})
	}
	return out, nil
}
func (e *Engine) EncodeTransaction(tx core.Transaction) ([]byte, error) {
	t, err := Unwrap(tx)
	if err != nil {
		return nil, err
	}
	if err := verify(t, e.network); err != nil {
		return nil, err
	}
	return json.Marshal(t)
}
func (e *Engine) DecodeTransaction(data []byte) (core.Transaction, error) {
	if len(data) > 1<<20 {
		return nil, fmt.Errorf("utxo transaction too large")
	}
	var t Transaction
	if err := jsoncodec.Canonical(data, &t); err != nil {
		return nil, err
	}

	tx, err := Wrap(t)
	if err != nil {
		return nil, err
	}
	if err := verify(t, e.network); err != nil {
		return nil, err
	}

	return tx, nil
}
func (e *Engine) ValidateTransaction(ctx context.Context, tx core.Transaction) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	t, err := Unwrap(tx)
	if err == nil {
		err = verify(t, e.network)
	}
	if err != nil {
		return fmt.Errorf("%w: %v", core.ErrInvalidTransaction, err)
	}
	return nil
}
func (e *Engine) Execute(ctx context.Context, parent core.StateReader, txs []core.Transaction) (model.Transition, error) {
	if e.maturity != 0 {
		return model.Transition{}, fmt.Errorf("utxo maturity requires height context")
	}
	return e.execute(ctx, parent, txs, 0)
}
func (e *Engine) ExecuteInContext(ctx context.Context, parent core.StateReader, txs []core.Transaction, execution model.ExecutionContext) (model.Transition, error) {
	h, ok := execution.(model.HeightContext)
	if !ok {
		return model.Transition{}, fmt.Errorf("utxo requires height context")
	}
	return e.execute(ctx, parent, txs, h.Height())
}
func (e *Engine) execute(ctx context.Context, parent core.StateReader, txs []core.Transaction, height uint64) (model.Transition, error) {
	state, err := parent.Entries(ctx)
	if err != nil {
		return model.Transition{}, err
	}
	state, err = core.ApplyStateChanges(ctx, state, nil, nil)
	if err != nil {
		return model.Transition{}, err
	}
	out := model.Transition{Writes: map[string][]byte{}}
	deleted := map[string]bool{}
	spent := map[string]bool{}
	var fees uint64
	seenTx := map[core.ID]bool{}
	for _, tx := range txs {
		if err := e.ValidateTransaction(ctx, tx); err != nil {
			return model.Transition{}, err
		}
		t, _ := Unwrap(tx)
		reject := func(d core.TransactionDisposition, cause string) (model.Transition, error) {
			return model.Transition{}, &core.TransactionError{TransactionID: tx.ID(), Disposition: d, Cause: fmt.Errorf("%s", cause)}
		}
		if seenTx[tx.ID()] {
			return reject(core.TransactionRejected, "duplicate transaction")
		}
		seenTx[tx.ID()] = true
		var inputs, outputs uint64
		for _, input := range t.Inputs {
			k := outputKey(input.Previous)
			data, exists := state[k]
			if !exists {
				if spent[k] {
					return reject(core.TransactionRejected, "in-block double spend")
				}
				return reject(core.TransactionDeferred, "input unavailable")
			}
			coin, err := decode(data)
			if err != nil {
				return model.Transition{}, err
			}
			if coin.To != asset.AddressOf(input.PublicKey) {
				return reject(core.TransactionRejected, "input ownership mismatch")
			}
			if metadata, exists := state[maturityKey(k)]; exists {
				if len(metadata) != 16 {
					return model.Transition{}, fmt.Errorf("corrupt coinbase metadata")
				}
				if height < binary.BigEndian.Uint64(metadata[8:]) {
					return reject(core.TransactionDeferred, "coinbase immature")
				}
				delete(state, maturityKey(k))
				deleted[maturityKey(k)] = true
			}
			inputs, err = asset.Add(inputs, coin.Amount)
			if err != nil {
				return reject(core.TransactionRejected, "input amount overflow")
			}
			delete(state, k)
			spent[k] = true
			// Outputs produced and consumed inside one execution need no persisted delete.
			if _, created := out.Writes[k]; created {
				delete(out.Writes, k)
			} else {
				deleted[k] = true
			}
		}
		points, _ := Outpoints(tx)
		for i, coin := range t.Outputs {
			outputs, err = asset.Add(outputs, coin.Amount)
			if err != nil {
				return reject(core.TransactionRejected, "output amount overflow")
			}
			k := outputKey(points[i])
			if _, exists := state[k]; exists || deleted[k] {
				return reject(core.TransactionRejected, "output collision")
			}
			state[k] = encode(coin)
			out.Writes[k] = bytes.Clone(state[k])
		}
		if outputs > inputs {
			return reject(core.TransactionRejected, "outputs exceed inputs")
		}
		fees, err = asset.Add(fees, inputs-outputs)
		if err != nil {
			return reject(core.TransactionRejected, "fee total overflow")
		}
		out.Receipts = append(out.Receipts, core.Receipt{TransactionID: tx.ID(), Data: []byte{1}})
	}
	for k := range deleted {
		out.Deletes = append(out.Deletes, k)
	}
	sort.Strings(out.Deletes)
	out.Effects = []model.Effect{asset.Fee(fees)}
	return out, nil
}
func (e *Engine) Credit(ctx context.Context, parent core.StateReader, mint assetcap.Mint) (model.Transition, error) {
	if e.maturity != 0 {
		return model.Transition{}, fmt.Errorf("utxo mint requires height context")
	}
	return e.CreditAtHeight(ctx, parent, mint, 0)
}
func (e *Engine) CreditAtHeight(ctx context.Context, parent core.StateReader, mint assetcap.Mint, height uint64) (model.Transition, error) {
	if err := ctx.Err(); err != nil {
		return model.Transition{}, err
	}
	if mint.ID == "" || mint.Amount == 0 {
		return model.Transition{}, fmt.Errorf("invalid mint")
	}
	hash := sha256.Sum256(append([]byte("gochainkit/utxo/mint/v1\x00"), []byte(mint.ID)...))
	k := outputKey(Outpoint{TransactionID: hash})
	state, err := parent.Entries(ctx)
	if err != nil {
		return model.Transition{}, err
	}
	marker := "model/utxo/mint/" + string(mint.ID)
	if _, exists := state[marker]; exists {
		return model.Transition{}, fmt.Errorf("mint already applied")
	}
	if _, exists := state[k]; exists {
		return model.Transition{}, fmt.Errorf("mint output collision")
	}
	if height > ^uint64(0)-e.maturity {
		return model.Transition{}, fmt.Errorf("coinbase maturity overflow")
	}
	writes := map[string][]byte{k: encode(Output{mint.To, mint.Amount}), marker: []byte{1}}
	if e.maturity != 0 {
		writes[maturityKey(k)] = binary.BigEndian.AppendUint64(binary.BigEndian.AppendUint64(nil, height), height+e.maturity)
	}
	return model.Transition{Writes: writes}, nil
}

var _ assetcap.Currency = (*Engine)(nil)
