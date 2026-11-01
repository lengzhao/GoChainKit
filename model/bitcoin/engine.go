// Package bitcoin connects native btcd transactions and validation to isolated
// framework UTXO snapshots. The enclosing block/consensus format is custom.
package bitcoin

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/btcsuite/btcd/blockchain"
	"github.com/btcsuite/btcd/btcutil/v2"
	"github.com/btcsuite/btcd/chaincfg/v2"
	"github.com/btcsuite/btcd/chainhash/v2"
	"github.com/btcsuite/btcd/txscript/v2"
	"github.com/btcsuite/btcd/wire/v2"
	adapter "github.com/lengzhao/GoChainKit/adapter/bitcoin"
	"github.com/lengzhao/GoChainKit/cap/model"
	"github.com/lengzhao/GoChainKit/core"
)

type Allocation struct {
	Value  int64  `json:"value,string"`
	Script []byte `json:"script"`
}
type Config struct {
	Network             string       `json:"network"`
	GenesisTime         int64        `json:"genesisTime"`
	Allocations         []Allocation `json:"allocations"`
	MaxTransactionBytes int          `json:"maxTransactionBytes"`
}

// Output includes the deterministic metadata needed for relative locks.
type Output struct {
	Value      int64  `json:"value,string"`
	Script     []byte `json:"script"`
	Height     int32  `json:"height"`
	MedianTime int64  `json:"medianTime"`
}

// Engine has immutable configuration; all execution state belongs to its snapshot.
// Supply is allocated at genesis, ordinary coinbase is forbidden, and fees burn.
type Engine struct {
	config     Config
	descriptor core.ProtocolDescriptor
	genesis    chainhash.Hash
	codec      *adapter.Transactions
}
type transaction struct {
	raw    []byte
	id     core.ID
	native *wire.MsgTx
}

func (t *transaction) ID() core.ID { return t.id }

func New(c Config) (*Engine, error) {
	if c.Network == "" || len(c.Network) > 128 || c.GenesisTime < 1 || c.GenesisTime > math.MaxInt64-(1<<26) || len(c.Allocations) > 1024 {
		return nil, fmt.Errorf("bitcoin model: invalid genesis configuration")
	}
	if c.MaxTransactionBytes == 0 {
		c.MaxTransactionBytes = 1 << 20
	}
	codec, err := adapter.New(adapter.Config{MaxTransactionBytes: c.MaxTransactionBytes})
	if err != nil {
		return nil, err
	}
	c.Allocations = append([]Allocation(nil), c.Allocations...)
	var supply int64
	for i, a := range c.Allocations {
		if a.Value < 1 || a.Value > btcutil.MaxSatoshi || supply > btcutil.MaxSatoshi-a.Value || len(a.Script) == 0 || len(a.Script) > txscript.MaxScriptSize || txscript.IsUnspendable(a.Script) {
			return nil, fmt.Errorf("bitcoin model: invalid allocation")
		}
		supply += a.Value
		c.Allocations[i].Script = bytes.Clone(a.Script)
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}
	binding := sha256.Sum256(append([]byte("gochainkit/bitcoin/btcd-0.26/v1/standard-flags/burn/"), raw...))
	return &Engine{config: c, descriptor: core.ProtocolDescriptor{Name: "bitcoin", Version: "1/" + hex.EncodeToString(binding[:]), Network: c.Network}, genesis: chainhash.Hash(binding), codec: codec}, nil
}
func (e *Engine) Protocol() core.ProtocolDescriptor { return e.descriptor }
func (e *Engine) GenesisOutpoints() []wire.OutPoint {
	out := make([]wire.OutPoint, len(e.config.Allocations))
	for i := range out {
		out[i] = wire.OutPoint{Hash: e.genesis, Index: uint32(i)}
	}
	return out
}
func outputKey(p wire.OutPoint) string {
	return "model/bitcoin/output/" + string(p.Hash[:]) + string(binary.BigEndian.AppendUint32(nil, p.Index))
}
func marker(id core.ID) string { return "model/bitcoin/seen/" + string(id) }
func (e *Engine) Genesis(ctx context.Context) (map[string][]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := map[string][]byte{}
	for i, p := range e.GenesisOutpoints() {
		a := e.config.Allocations[i]
		raw, _ := json.Marshal(Output{a.Value, a.Script, 0, e.config.GenesisTime})
		out[outputKey(p)] = raw
	}
	return out, nil
}
func decodeOutput(raw []byte) (Output, error) {
	var o Output
	if err := json.Unmarshal(raw, &o); err != nil {
		return Output{}, fmt.Errorf("bitcoin model corrupt output: %w", err)
	}
	if o.Value < 0 || o.Value > btcutil.MaxSatoshi || o.Height < 0 || o.MedianTime < 0 || o.MedianTime > math.MaxInt64-(1<<26) || len(o.Script) > txscript.MaxScriptSize {
		return Output{}, fmt.Errorf("bitcoin model: corrupt output metadata")
	}
	return o, nil
}

// Output reads a caller-owned native-script UTXO at the supplied state anchor.
func (e *Engine) Output(ctx context.Context, s core.StateReader, p wire.OutPoint) (Output, error) {
	raw, err := s.Get(ctx, []byte(outputKey(p)))
	if err != nil {
		return Output{}, err
	}
	return decodeOutput(raw)
}
func (e *Engine) DecodeTransaction(raw []byte) (core.Transaction, error) {
	native, err := e.codec.Decode(raw)
	if err != nil {
		return nil, err
	}
	if err = blockchain.CheckTransactionSanity(btcutil.NewTx(native)); err != nil {
		return nil, err
	}
	if blockchain.IsCoinBaseTx(native) {
		return nil, fmt.Errorf("bitcoin model: coinbase not allowed")
	}
	hash := native.TxHash()
	return &transaction{bytes.Clone(raw), core.ID(string(hash[:])), native}, nil
}
func (e *Engine) EncodeTransaction(tx core.Transaction) ([]byte, error) {
	t, ok := tx.(*transaction)
	if !ok || t == nil {
		return nil, fmt.Errorf("bitcoin model: foreign transaction")
	}
	return bytes.Clone(t.raw), nil
}

// Native returns an owned native transaction; mutation cannot change its wrapper.
func (e *Engine) Native(tx core.Transaction) (*wire.MsgTx, error) {
	t, ok := tx.(*transaction)
	if !ok || t == nil || t.native == nil {
		return nil, fmt.Errorf("bitcoin model: foreign transaction")
	}
	if len(t.raw) > e.config.MaxTransactionBytes {
		return nil, fmt.Errorf("bitcoin model: transaction exceeds limit")
	}
	return t.native.Copy(), nil
}
func (e *Engine) ValidateTransaction(ctx context.Context, tx core.Transaction) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	t, ok := tx.(*transaction)
	if !ok || t == nil || t.native == nil || len(t.raw) > e.config.MaxTransactionBytes {
		return fmt.Errorf("%w: invalid bitcoin transaction or size", core.ErrInvalidTransaction)
	}
	// The private wrapper is canonical and sanity-checked at construction. Execution
	// still checks state-dependent amounts, scripts, spendability and lock rules.
	return nil
}
func (e *Engine) Execute(context.Context, core.StateReader, []core.Transaction) (model.Transition, error) {
	return model.Transition{}, fmt.Errorf("bitcoin model: height and median time context required")
}

func (e *Engine) ExecuteInContext(ctx context.Context, parent core.StateReader, txs []core.Transaction, execution model.ExecutionContext) (model.Transition, error) {
	h, ok := execution.(model.HeightContext)
	if !ok || h.Height() > math.MaxInt32 {
		return model.Transition{}, fmt.Errorf("bitcoin model: supported height context required")
	}
	clock, ok := execution.(model.MedianTimeContext)
	if !ok {
		return model.Transition{}, fmt.Errorf("bitcoin model: median time context required")
	}
	median, err := clock.MedianTimePast(ctx)
	if err != nil {
		return model.Transition{}, err
	}
	if median < 0 || median > math.MaxInt64-(1<<26) {
		return model.Transition{}, fmt.Errorf("bitcoin model: invalid median time")
	}
	initial, err := parent.Entries(ctx)
	if err != nil {
		return model.Transition{}, err
	}
	state, err := core.ApplyStateChanges(ctx, initial, nil, nil)
	if err != nil {
		return model.Transition{}, err
	}
	out := model.Transition{Writes: map[string][]byte{}}
	spent := map[wire.OutPoint]bool{}
	height := int32(h.Height())
	for _, tx := range txs {
		if err = e.ValidateTransaction(ctx, tx); err != nil {
			return model.Transition{}, err
		}
		native, err := e.Native(tx)
		if err != nil {
			return model.Transition{}, err
		}
		reject := func(d core.TransactionDisposition, cause error) (model.Transition, error) {
			return model.Transition{}, &core.TransactionError{TransactionID: tx.ID(), Disposition: d, Cause: cause}
		}
		if _, seen := state[marker(tx.ID())]; seen {
			return reject(core.TransactionRejected, fmt.Errorf("bitcoin transaction already executed"))
		}
		util := btcutil.NewTx(native)
		if !blockchain.IsFinalizedTransaction(util, height, time.Unix(median, 0)) {
			return reject(core.TransactionDeferred, fmt.Errorf("bitcoin transaction is time locked"))
		}
		view := blockchain.NewUtxoViewpoint()
		prevouts := make(map[wire.OutPoint]*wire.TxOut, len(native.TxIn))
		locks := blockchain.SequenceLock{Seconds: -1, BlockHeight: -1}
		for _, in := range native.TxIn {
			raw, exists := state[outputKey(in.PreviousOutPoint)]
			if !exists {
				d := core.TransactionDeferred
				_, known := state[marker(core.ID(string(in.PreviousOutPoint.Hash[:])))]
				if spent[in.PreviousOutPoint] || known || in.PreviousOutPoint.Hash == e.genesis {
					d = core.TransactionRejected
				}
				return reject(d, fmt.Errorf("bitcoin input unavailable"))
			}
			coin, err := decodeOutput(raw)
			if err != nil {
				return model.Transition{}, err
			}
			if coin.Height > height {
				return model.Transition{}, fmt.Errorf("bitcoin model: future UTXO metadata")
			}
			nativeOut := wire.NewTxOut(coin.Value, coin.Script)
			view.Entries()[in.PreviousOutPoint] = blockchain.NewUtxoEntry(nativeOut, coin.Height, false)
			prevouts[in.PreviousOutPoint] = nativeOut
			if native.Version >= 2 && in.Sequence&wire.SequenceLockTimeDisabled == 0 {
				value := int64(in.Sequence & wire.SequenceLockTimeMask)
				if in.Sequence&wire.SequenceLockTimeIsSeconds != 0 {
					required := coin.MedianTime + (value << wire.SequenceLockTimeGranularity) - 1
					if required > locks.Seconds {
						locks.Seconds = required
					}
				} else {
					required := int64(coin.Height) + value - 1
					if required > math.MaxInt32 {
						return reject(core.TransactionDeferred, fmt.Errorf("bitcoin relative height exceeds supported height"))
					}
					if required > int64(locks.BlockHeight) {
						locks.BlockHeight = int32(required)
					}
				}
			}
		}
		if !blockchain.SequenceLockActive(&locks, height, time.Unix(median, 0)) {
			return reject(core.TransactionDeferred, fmt.Errorf("bitcoin sequence lock active"))
		}
		fee, err := blockchain.CheckTransactionInputs(util, height, view, &chaincfg.RegressionNetParams)
		if err != nil {
			return reject(core.TransactionRejected, err)
		}
		if err = e.codec.VerifyScripts(ctx, native, prevouts); err != nil {
			if ctx.Err() != nil {
				return model.Transition{}, ctx.Err()
			}
			return reject(core.TransactionRejected, err)
		}
		for _, in := range native.TxIn {
			delete(state, outputKey(in.PreviousOutPoint))
			spent[in.PreviousOutPoint] = true
		}
		hash := native.TxHash()
		for i, coin := range native.TxOut {
			if txscript.IsUnspendable(coin.PkScript) {
				continue
			}
			point := wire.OutPoint{Hash: hash, Index: uint32(i)}
			key := outputKey(point)
			if _, exists := state[key]; exists {
				return reject(core.TransactionRejected, fmt.Errorf("bitcoin output collision"))
			}
			raw, _ := json.Marshal(Output{coin.Value, coin.PkScript, height, median})
			state[key] = raw
		}
		state[marker(tx.ID())] = []byte{1}
		receipt, _ := json.Marshal(struct {
			Fee int64 `json:"fee,string"`
		}{fee})
		out.Receipts = append(out.Receipts, core.Receipt{TransactionID: tx.ID(), Data: receipt})
	}
	for k, v := range state {
		if before, exists := initial[k]; !exists || !bytes.Equal(before, v) {
			out.Writes[k] = bytes.Clone(v)
		}
	}
	for k := range initial {
		if _, exists := state[k]; !exists {
			out.Deletes = append(out.Deletes, k)
		}
	}
	sort.Strings(out.Deletes)
	return out, ctx.Err()
}

var _ model.ContextualEngine = (*Engine)(nil)
