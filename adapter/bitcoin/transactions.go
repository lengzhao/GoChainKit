// Package bitcoin adapts btcd transaction encoding and script execution without
// changing native types, signature rules or transaction identifiers.
package bitcoin

import (
	"bytes"
	"context"
	"fmt"

	"github.com/btcsuite/btcd/txscript/v2"
	"github.com/btcsuite/btcd/wire/v2"
)

// Config controls local resource bounds and explicit script policy. Nil flags
// select btcd StandardVerifyFlags; these are policy, not full chain consensus.
type Config struct {
	MaxTransactionBytes int                   `json:"maxTransactionBytes"`
	ScriptFlags         *txscript.ScriptFlags `json:"scriptFlags"`
}

// Transactions is immutable and safe for concurrent calls. Callers must not
// concurrently mutate native inputs or prevouts. It owns no external resources.
type Transactions struct {
	maxBytes int
	flags    txscript.ScriptFlags
}

func New(c Config) (*Transactions, error) {
	if c.MaxTransactionBytes == 0 {
		c.MaxTransactionBytes = 4 << 20
	}
	if c.MaxTransactionBytes < 1 || c.MaxTransactionBytes > 4<<20 {
		return nil, fmt.Errorf("bitcoin: transaction limit must be 1..4194304")
	}
	flags := txscript.StandardVerifyFlags
	if c.ScriptFlags != nil {
		flags = *c.ScriptFlags
	}
	return &Transactions{c.MaxTransactionBytes, flags}, nil
}

// Encode returns caller-owned Bitcoin wire bytes, including witness. Native
// object shape and size are checked; this does not validate spendability.
func (s *Transactions) Encode(tx *wire.MsgTx) ([]byte, error) {
	if tx == nil {
		return nil, fmt.Errorf("bitcoin: transaction required")
	}
	for _, in := range tx.TxIn {
		if in == nil {
			return nil, fmt.Errorf("bitcoin: nil input")
		}
	}
	for _, out := range tx.TxOut {
		if out == nil {
			return nil, fmt.Errorf("bitcoin: nil output")
		}
	}
	if tx.SerializeSize() > s.maxBytes {
		return nil, fmt.Errorf("bitcoin: transaction exceeds limit")
	}
	var b bytes.Buffer
	if err := tx.Serialize(&b); err != nil {
		return nil, fmt.Errorf("bitcoin encode: %w", err)
	}
	return b.Bytes(), nil
}

// Decode rejects noncanonical encodings and trailing bytes. It returns a native
// transaction owning its bytes; no generic framework transaction is introduced.
func (s *Transactions) Decode(data []byte) (*wire.MsgTx, error) {
	if len(data) == 0 || len(data) > s.maxBytes {
		return nil, fmt.Errorf("bitcoin: invalid transaction size")
	}
	tx := wire.NewMsgTx(2)
	r := bytes.NewReader(data)
	if err := tx.Deserialize(r); err != nil {
		return nil, fmt.Errorf("bitcoin decode: %w", err)
	}
	encoded, err := s.Encode(tx)
	if err != nil {
		return nil, err
	}
	if r.Len() != 0 || !bytes.Equal(data, encoded) {
		return nil, fmt.Errorf("bitcoin: noncanonical transaction or trailing bytes")
	}
	return tx, nil
}

// VerifyScripts executes every input against caller-supplied prevouts. All inputs
// must have a nonnegative-valued prevout, including for Taproot sighash precompute.
// It proves neither UTXO existence/unspentness nor fees, maturity or block validity.
// Cancellation is checked between bounded upstream operations.
func (s *Transactions) VerifyScripts(ctx context.Context, tx *wire.MsgTx, prevouts map[wire.OutPoint]*wire.TxOut) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := s.Encode(tx); err != nil {
		return err
	}
	if len(tx.TxIn) == 0 {
		return fmt.Errorf("bitcoin: inputs required")
	}
	seen := make(map[wire.OutPoint]bool, len(tx.TxIn))
	for _, in := range tx.TxIn {
		prev := prevouts[in.PreviousOutPoint]
		if seen[in.PreviousOutPoint] || prev == nil || prev.Value < 0 || len(prev.PkScript) > txscript.MaxScriptSize {
			return fmt.Errorf("bitcoin: missing or invalid prevout, or duplicate input")
		}
		seen[in.PreviousOutPoint] = true
	}
	fetcher := txscript.NewMultiPrevOutFetcher(prevouts)
	hashes := txscript.NewTxSigHashes(tx, fetcher)
	for i, in := range tx.TxIn {
		if err := ctx.Err(); err != nil {
			return err
		}
		prev := prevouts[in.PreviousOutPoint]
		engine, err := txscript.NewEngine(prev.PkScript, tx, i, s.flags, nil, hashes, prev.Value, fetcher)
		if err != nil {
			return fmt.Errorf("bitcoin input %d: %w", i, err)
		}
		if err = engine.Execute(); err != nil {
			return fmt.Errorf("bitcoin input %d: %w", i, err)
		}
	}
	return ctx.Err()
}
