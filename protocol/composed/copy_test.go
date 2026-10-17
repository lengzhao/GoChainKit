package composed_test

import (
	"bytes"
	"encoding/json"
	"github.com/lengzhao/GoChainKit/protocol/composed"
	"testing"
)

func TestEnvelopeCopyPreservesEncodingAndIsolation(t *testing.T) {
	for _, txs := range [][][]byte{nil, {}, {nil, {}, {1, 2}}} {
		original := composed.Envelope{Header: composed.Header{Version: 1, Binding: make([]byte, 32), Parent: []byte{1}, TransactionsRoot: []byte{}, StateRoot: []byte{2}, ReceiptsRoot: []byte{3}, Producer: []byte{4}, Consensus: []byte{5}}, Transactions: txs, Proof: []byte{6}}
		encoded, _ := json.Marshal(original)
		block, err := composed.Wrap(original)
		if err != nil {
			t.Fatal(err)
		}
		id := block.ID()
		original.Header.Binding[0] = 9
		original.Header.Parent[0] = 9
		original.Proof[0] = 9
		if len(txs) > 2 {
			txs[2][0] = 9
		}
		copied, err := composed.Unwrap(block)
		if err != nil {
			t.Fatal(err)
		}
		actual, _ := json.Marshal(copied)
		if !bytes.Equal(encoded, actual) || block.ID() != id {
			t.Fatal("copy changed encoding or shared input")
		}
		copied.Header.Consensus[0] = 9
		copied.Proof[0] = 9
		copiedAgain, _ := composed.Unwrap(block)
		actual, _ = json.Marshal(copiedAgain)
		if !bytes.Equal(encoded, actual) {
			t.Fatal("unwrap leaked mutation")
		}
	}
}
