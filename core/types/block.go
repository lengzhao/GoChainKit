package types

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
)

// TransactionRoot commits to the ordered transaction IDs. This v1 commitment
// is a domain-separated list hash, not a Merkle inclusion-proof construction.
func TransactionRoot(txs []Transaction) (Hash, error) {
	var b bytes.Buffer
	b.WriteString("gochainkit/transactions/v1\x00")
	for _, tx := range txs {
		id, err := tx.ID()
		if err != nil {
			return Hash{}, err
		}
		b.Write(id[:])
	}
	return sha256.Sum256(b.Bytes()), nil
}
func ReceiptRoot(receipts []Receipt) Hash {
	var b bytes.Buffer
	b.WriteString("gochainkit/receipts/v1\x00")
	for _, r := range receipts {
		b.Write(r.TransactionID[:])
		if r.Success {
			b.WriteByte(1)
		} else {
			b.WriteByte(0)
		}
		field(&b, []byte(r.ErrorCode))
	}
	return sha256.Sum256(b.Bytes())
}

// ID validates the body commitment and hashes the canonical header. Execution
// and consensus validation of state/receipts remain the caller's responsibility.
func (b Block) ID() (Hash, error) {
	h := b.Header
	if h.Version != 1 || h.ChainID == "" || len(h.ChainID) > MaxChainIDBytes {
		return Hash{}, errors.New("invalid block header")
	}
	root, err := TransactionRoot(b.Transactions)
	if err != nil {
		return Hash{}, err
	}
	if root != h.TransactionRoot {
		return Hash{}, errors.New("transaction root mismatch")
	}
	var out bytes.Buffer
	out.WriteString("gochainkit/block/v1\x00")
	_ = binary.Write(&out, binary.BigEndian, h.Version)
	field(&out, []byte(h.ChainID))
	_ = binary.Write(&out, binary.BigEndian, h.Height)
	out.Write(h.Parent[:])
	out.Write(h.TransactionRoot[:])
	out.Write(h.StateRoot[:])
	out.Write(h.ReceiptRoot[:])
	return sha256.Sum256(out.Bytes()), nil
}
func (b Block) Clone() Block {
	txs := make([]Transaction, len(b.Transactions))
	for i, tx := range b.Transactions {
		txs[i] = tx.Clone()
	}
	b.Transactions = txs
	return b
}
