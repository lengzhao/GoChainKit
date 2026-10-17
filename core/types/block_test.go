package types

import (
	"crypto/ed25519"
	"testing"
)

func TestBlockCommitments(t *testing.T) {
	tx := Transaction{ChainID: "dev", Nonce: 1, Key: []byte("a"), Value: []byte("b")}
	if err := tx.Sign(ed25519.NewKeyFromSeed(make([]byte, 32))); err != nil {
		t.Fatal(err)
	}
	root, err := TransactionRoot([]Transaction{tx})
	if err != nil {
		t.Fatal(err)
	}
	b := Block{Header: BlockHeader{Version: 1, ChainID: "dev", Height: 1, TransactionRoot: root}, Transactions: []Transaction{tx}}
	first, err := b.ID()
	if err != nil {
		t.Fatal(err)
	}
	b.Header.Height++
	next, err := b.ID()
	if err != nil {
		t.Fatal(err)
	}
	if first == next {
		t.Fatal("height is not committed")
	}
	b.Transactions[0].Value[0] = 'x'
	if _, err := b.ID(); err == nil {
		t.Fatal("accepted body/root mismatch")
	}
}
