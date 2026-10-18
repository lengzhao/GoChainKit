// Package account implements signed balance/nonce transfers independently of consensus.
package account

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
)

type Config struct {
	Network     string
	Allocations []assetcap.Allocation
}
type Engine struct {
	network     string
	allocations []assetcap.Allocation
}
type Transaction struct {
	Network              string
	Nonce                uint64
	To                   assetcap.Address
	Amount, Fee          uint64
	PublicKey, Signature []byte
}
type transaction struct {
	value Transaction
	id    core.ID
}

func (t *transaction) ID() core.ID { return t.id }
func (t Transaction) clone() Transaction {
	t.PublicKey = bytes.Clone(t.PublicKey)
	t.Signature = bytes.Clone(t.Signature)
	return t
}
func (t Transaction) payload() ([]byte, error) {
	if t.Network == "" || len(t.Network) > 128 || t.Nonce == 0 || t.Amount == 0 || len(t.PublicKey) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("invalid account transaction")
	}
	if _, err := asset.Add(t.Amount, t.Fee); err != nil {
		return nil, err
	}
	t.Signature = nil
	data, err := json.Marshal(t)
	return append([]byte("gochainkit/account/sign/v1\x00"), data...), err
}
func (t *Transaction) Sign(key ed25519.PrivateKey) error {
	if t == nil || len(key) != ed25519.PrivateKeySize || !bytes.Equal(ed25519.NewKeyFromSeed(key[:32]), key) {
		return fmt.Errorf("invalid private key")
	}
	t.PublicKey = bytes.Clone(key[32:])
	payload, err := t.payload()
	if err != nil {
		return err
	}
	t.Signature = ed25519.Sign(key, payload)
	return nil
}
func verify(t Transaction, network string) error {
	if t.Network != network {
		return fmt.Errorf("wrong account network")
	}
	payload, err := t.payload()
	if err != nil {
		return err
	}
	if len(t.Signature) != ed25519.SignatureSize || !ed25519.Verify(t.PublicKey, payload, t.Signature) {
		return fmt.Errorf("invalid account signature")
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
	id := sha256.Sum256(append([]byte("gochainkit/account/tx/v1\x00"), data...))
	return &transaction{value: t.clone(), id: core.ID(string(id[:]))}, nil
}
func Unwrap(t core.Transaction) (Transaction, error) {
	v, ok := t.(*transaction)
	if !ok || v == nil {
		return Transaction{}, fmt.Errorf("not an account transaction")
	}
	return v.value.clone(), nil
}
func New(c Config) (*Engine, error) {
	if c.Network == "" || len(c.Network) > 128 {
		return nil, fmt.Errorf("account network required")
	}
	seen := map[assetcap.Address]bool{}
	var supply uint64
	for _, a := range c.Allocations {
		if seen[a.Address] || a.Amount == 0 {
			return nil, fmt.Errorf("invalid genesis allocation")
		}
		seen[a.Address] = true
		var err error
		supply, err = asset.Add(supply, a.Amount)
		if err != nil {
			return nil, err
		}
	}
	return &Engine{c.Network, append([]assetcap.Allocation(nil), c.Allocations...)}, nil
}
func (e *Engine) Protocol() core.ProtocolDescriptor {
	return core.ProtocolDescriptor{Name: "account", Version: "1", Network: e.network}
}
func key(address assetcap.Address) string { return "model/account/address/" + string(address[:]) }
func encodeAccount(balance, nonce uint64) []byte {
	return binary.BigEndian.AppendUint64(binary.BigEndian.AppendUint64(nil, balance), nonce)
}
func read(state map[string][]byte, address assetcap.Address) (uint64, uint64, error) {
	data, ok := state[key(address)]
	if !ok {
		return 0, 0, nil
	}
	if len(data) != 16 {
		return 0, 0, fmt.Errorf("corrupt account record")
	}
	return binary.BigEndian.Uint64(data[:8]), binary.BigEndian.Uint64(data[8:]), nil
}

// Account contains the balance and sequence at one state snapshot.
type Account struct{ Balance, Nonce uint64 }

// ReadAccount reads one address without enumerating unrelated state. Missing
// addresses have zero balance and nonce, as in transaction execution.
func ReadAccount(ctx context.Context, s core.StateReader, address assetcap.Address) (Account, error) {
	data, err := s.Get(ctx, []byte(key(address)))
	if err == core.ErrNotFound {
		return Account{}, nil
	}
	if err != nil {
		return Account{}, err
	}
	if len(data) != 16 {
		return Account{}, fmt.Errorf("corrupt account record")
	}
	return Account{binary.BigEndian.Uint64(data[:8]), binary.BigEndian.Uint64(data[8:])}, nil
}
func Balance(ctx context.Context, s core.StateReader, address assetcap.Address) (uint64, error) {
	a, err := ReadAccount(ctx, s, address)
	return a.Balance, err
}
func (e *Engine) Genesis(ctx context.Context) (map[string][]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	state := map[string][]byte{}
	for _, a := range e.allocations {
		state[key(a.Address)] = encodeAccount(a.Amount, 0)
	}
	return state, nil
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
	if len(data) > 4096 {
		return nil, fmt.Errorf("account transaction too large")
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
	state, err := parent.Entries(ctx)
	if err != nil {
		return model.Transition{}, err
	}
	state, err = core.ApplyStateChanges(ctx, state, nil, nil)
	if err != nil {
		return model.Transition{}, err
	}
	out := model.Transition{Writes: map[string][]byte{}}
	var fees uint64
	for _, tx := range txs {
		if err := e.ValidateTransaction(ctx, tx); err != nil {
			return model.Transition{}, err
		}
		t, _ := Unwrap(tx)
		sender := asset.AddressOf(t.PublicKey)
		balance, nonce, err := read(state, sender)
		if err != nil {
			return model.Transition{}, err
		}
		reject := func(disposition core.TransactionDisposition, cause string) (model.Transition, error) {
			return model.Transition{}, &core.TransactionError{TransactionID: tx.ID(), Disposition: disposition, Cause: fmt.Errorf("%s", cause)}
		}
		if nonce == ^uint64(0) || t.Nonce <= nonce {
			return reject(core.TransactionRejected, "nonce already consumed")
		}
		if t.Nonce != nonce+1 {
			return reject(core.TransactionDeferred, "nonce predecessor missing")
		}
		cost, _ := asset.Add(t.Amount, t.Fee)
		if balance < cost {
			return reject(core.TransactionDeferred, "insufficient balance")
		}
		// Apply sender first so a self-transfer debits only the fee.
		state[key(sender)] = encodeAccount(balance-cost, t.Nonce)
		receiverBalance, receiverNonce, err := read(state, t.To)
		if err != nil {
			return model.Transition{}, err
		}
		receiverBalance, err = asset.Add(receiverBalance, t.Amount)
		if err != nil {
			return reject(core.TransactionRejected, "recipient balance overflow")
		}
		state[key(t.To)] = encodeAccount(receiverBalance, receiverNonce)
		out.Writes[key(sender)] = bytes.Clone(state[key(sender)])
		out.Writes[key(t.To)] = bytes.Clone(state[key(t.To)])
		fees, err = asset.Add(fees, t.Fee)
		if err != nil {
			return reject(core.TransactionRejected, "fee total overflow")
		}
		out.Receipts = append(out.Receipts, core.Receipt{TransactionID: tx.ID(), Data: []byte{1}})
	}
	out.Effects = []model.Effect{asset.Fee(fees)}
	return out, nil
}
func (e *Engine) Credit(ctx context.Context, parent core.StateReader, mint assetcap.Mint) (model.Transition, error) {
	if err := ctx.Err(); err != nil {
		return model.Transition{}, err
	}
	if mint.ID == "" || mint.Amount == 0 {
		return model.Transition{}, fmt.Errorf("invalid mint")
	}
	state, err := parent.Entries(ctx)
	if err != nil {
		return model.Transition{}, err
	}
	marker := "model/account/mint/" + string(mint.ID)
	if _, exists := state[marker]; exists {
		return model.Transition{}, fmt.Errorf("mint already applied")
	}
	balance, nonce, err := read(state, mint.To)
	if err != nil {
		return model.Transition{}, err
	}
	balance, err = asset.Add(balance, mint.Amount)
	if err != nil {
		return model.Transition{}, err
	}
	return model.Transition{Writes: map[string][]byte{key(mint.To): encodeAccount(balance, nonce), marker: []byte{1}}}, nil
}

var _ assetcap.Currency = (*Engine)(nil)
