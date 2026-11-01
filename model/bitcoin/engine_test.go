package bitcoin_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/txscript/v2"
	"github.com/btcsuite/btcd/wire/v2"
	"github.com/lengzhao/GoChainKit/cap/model"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/GoChainKit/internal/modeltest"
	btc "github.com/lengzhao/GoChainKit/model/bitcoin"
)

type execution struct {
	height uint64
	median int64
}

func (e execution) Parent() core.Block                            { return nil }
func (e execution) Candidate() core.Block                         { return nil }
func (e execution) History() model.BlockHistory                   { return nil }
func (e execution) Height() uint64                                { return e.height }
func (e execution) Timestamp() int64                              { return e.median + 1 }
func (e execution) MedianTimePast(context.Context) (int64, error) { return e.median, nil }

func setup(t *testing.T) (*btc.Engine, modeltest.Reader, *btcec.PrivateKey, []byte) {
	t.Helper()
	key, _ := btcec.PrivKeyFromBytes(bytes.Repeat([]byte{1}, 32))
	script, err := txscript.NewScriptBuilder().AddData(key.PubKey().SerializeCompressed()).AddOp(txscript.OP_CHECKSIG).Script()
	if err != nil {
		t.Fatal(err)
	}
	e, err := btc.New(btc.Config{Network: "btc-test", GenesisTime: 1700000000, Allocations: []btc.Allocation{{Value: 100000, Script: script}}})
	if err != nil {
		t.Fatal(err)
	}
	state, err := e.Genesis(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return e, modeltest.Reader(state), key, script
}
func spend(t *testing.T, e *btc.Engine, point wire.OutPoint, value int64, key *btcec.PrivateKey, script []byte, lock uint32, seq uint32) core.Transaction {
	t.Helper()
	tx := wire.NewMsgTx(2)
	tx.LockTime = lock
	in := wire.NewTxIn(&point, nil, nil)
	in.Sequence = seq
	tx.AddTxIn(in)
	tx.AddTxOut(wire.NewTxOut(value, script))
	sig, err := txscript.RawTxInSignature(tx, 0, script, txscript.SigHashAll, key)
	if err != nil {
		t.Fatal(err)
	}
	tx.TxIn[0].SignatureScript, err = txscript.NewScriptBuilder().AddData(sig).Script()
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err = tx.Serialize(&buf); err != nil {
		t.Fatal(err)
	}
	frozen, err := e.DecodeTransaction(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	return frozen
}

func TestNativeSpendBatchAndIsolation(t *testing.T) {
	e, parent, key, script := setup(t)
	ctx := context.Background()
	point := e.GenesisOutpoints()[0]
	first := spend(t, e, point, 90000, key, script, 0, wire.MaxTxInSequenceNum)
	encoded, err := e.EncodeTransaction(first)
	if err != nil {
		t.Fatal(err)
	}
	native, err := e.Native(first)
	if err != nil {
		t.Fatal(err)
	}
	hash := native.TxHash()
	if first.ID() != core.ID(string(hash[:])) {
		t.Fatal("native txid changed")
	}
	child := spend(t, e, wire.OutPoint{Hash: native.TxHash(), Index: 0}, 80000, key, script, 0, wire.MaxTxInSequenceNum)
	tr, err := e.ExecuteInContext(ctx, parent, []core.Transaction{first, child}, execution{1, 1700000000})
	if err != nil {
		t.Fatal(err)
	}
	next, err := core.ApplyStateChanges(ctx, parent, tr.Writes, tr.Deletes)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Output(ctx, modeltest.Reader(next), point); !errors.Is(err, core.ErrNotFound) {
		t.Fatal("spent genesis remains")
	}
	if out, err := e.Output(ctx, parent, point); err != nil || out.Value != 100000 {
		t.Fatal("parent mutated")
	}
	if len(tr.Receipts) != 2 {
		t.Fatal("missing receipts")
	}
	duplicate := spend(t, e, point, 85000, key, script, 0, wire.MaxTxInSequenceNum)
	if _, err = e.ExecuteInContext(ctx, parent, []core.Transaction{first, duplicate}, execution{1, 1700000000}); !errors.Is(err, core.ErrInvalidTransaction) {
		t.Fatalf("double spend: %v", err)
	}
	if _, err = e.ExecuteInContext(ctx, modeltest.Reader(next), []core.Transaction{first}, execution{2, 1700000001}); !errors.Is(err, core.ErrInvalidTransaction) {
		t.Fatalf("replay: %v", err)
	}
	if _, err = e.ExecuteInContext(ctx, modeltest.Reader(next), []core.Transaction{duplicate}, execution{2, 1700000001}); !errors.Is(err, core.ErrInvalidTransaction) {
		t.Fatalf("cross-block double spend: %v", err)
	}
	native.TxOut[0].Value = 1
	frozen, _ := e.EncodeTransaction(first)
	if !bytes.Equal(encoded, frozen) {
		t.Fatal("native accessor aliases transaction")
	}
}

func TestNativeValueSignatureAndLockRules(t *testing.T) {
	e, parent, key, script := setup(t)
	ctx := context.Background()
	point := e.GenesisOutpoints()[0]
	for _, tc := range []struct {
		name      string
		value     int64
		lock, seq uint32
		at        execution
		want      error
	}{
		{"overspend", 100001, 0, wire.MaxTxInSequenceNum, execution{1, 1700000000}, core.ErrInvalidTransaction},
		{"absolute-height", 90000, 2, 0, execution{1, 1700000000}, core.ErrDeferredTransaction},
		{"relative-height", 90000, 0, 2, execution{1, 1700000000}, core.ErrDeferredTransaction},
		{"relative-time", 90000, 0, wire.SequenceLockTimeIsSeconds | 1, execution{1, 1700000000}, core.ErrDeferredTransaction},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx := spend(t, e, point, tc.value, key, script, tc.lock, tc.seq)
			if _, err := e.ExecuteInContext(ctx, parent, []core.Transaction{tx}, tc.at); !errors.Is(err, tc.want) {
				t.Fatalf("want %v: %v", tc.want, err)
			}
		})
	}
	locked := spend(t, e, point, 90000, key, script, 2, 0)
	if _, err := e.ExecuteInContext(ctx, parent, []core.Transaction{locked}, execution{3, 1700000003}); err != nil {
		t.Fatal(err)
	}
	badkey, _ := btcec.PrivKeyFromBytes(bytes.Repeat([]byte{2}, 32))
	bad := spend(t, e, point, 90000, badkey, script, 0, wire.MaxTxInSequenceNum)
	if _, err := e.ExecuteInContext(ctx, parent, []core.Transaction{bad}, execution{1, 1700000000}); !errors.Is(err, core.ErrInvalidTransaction) {
		t.Fatalf("bad signature: %v", err)
	}
	missing := point
	missing.Hash[0] ^= 1
	unknown := spend(t, e, missing, 90000, key, script, 0, wire.MaxTxInSequenceNum)
	if _, err := e.ExecuteInContext(ctx, parent, []core.Transaction{unknown}, execution{1, 1700000000}); !errors.Is(err, core.ErrDeferredTransaction) {
		t.Fatalf("missing input: %v", err)
	}
	if _, err := e.Execute(ctx, parent, nil); err == nil {
		t.Fatal("context-free execution accepted")
	}
}
