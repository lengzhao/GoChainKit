package bitcoin_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/txscript/v2"
	"github.com/btcsuite/btcd/wire/v2"
	"github.com/lengzhao/GoChainKit/adapter/bitcoin"
)

func signed(t *testing.T) (*wire.MsgTx, map[wire.OutPoint]*wire.TxOut) {
	t.Helper()
	key, _ := btcec.PrivKeyFromBytes(bytes.Repeat([]byte{1}, 32))
	script, err := txscript.NewScriptBuilder().AddData(key.PubKey().SerializeCompressed()).AddOp(txscript.OP_CHECKSIG).Script()
	if err != nil {
		t.Fatal(err)
	}
	point := wire.OutPoint{Index: 1}
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(wire.NewTxIn(&point, nil, nil))
	tx.AddTxOut(wire.NewTxOut(900, []byte{txscript.OP_TRUE}))
	tx.TxIn[0].SignatureScript, err = txscript.SignatureScript(tx, 0, script, txscript.SigHashAll, key, true)
	// SignatureScript adds a public key for P2PKH; P2PK needs only the signature.
	if err != nil {
		t.Fatal(err)
	}
	pushes, err := txscript.PushedData(tx.TxIn[0].SignatureScript)
	if err != nil {
		t.Fatal(err)
	}
	tx.TxIn[0].SignatureScript, err = txscript.NewScriptBuilder().AddData(pushes[0]).Script()
	if err != nil {
		t.Fatal(err)
	}
	return tx, map[wire.OutPoint]*wire.TxOut{point: wire.NewTxOut(1000, script)}
}

func TestNativeRoundTripAndScriptValidation(t *testing.T) {
	s, err := bitcoin.New(bitcoin.Config{})
	if err != nil {
		t.Fatal(err)
	}
	tx, prev := signed(t)
	raw, err := s.Encode(tx)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := s.Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.TxHash() != tx.TxHash() {
		t.Fatal("transaction hash changed")
	}
	if err = s.VerifyScripts(context.Background(), decoded, prev); err != nil {
		t.Fatal(err)
	}
	decoded.TxOut[0].Value++
	if err = s.VerifyScripts(context.Background(), decoded, prev); err == nil {
		t.Fatal("accepted changed signed output")
	}
	decoded.TxOut[0].Value--
	if err = s.VerifyScripts(context.Background(), decoded, nil); err == nil {
		t.Fatal("accepted missing prevout")
	}
	if _, err = s.Decode(append(raw, 0)); err == nil {
		t.Fatal("accepted trailing bytes")
	}
	if _, err = s.Decode(nil); err == nil {
		t.Fatal("accepted empty transaction")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err = s.VerifyScripts(ctx, tx, prev); err != context.Canceled {
		t.Fatalf("cancellation: %v", err)
	}
}

func TestBoundsAndInvalidNativeValues(t *testing.T) {
	s, _ := bitcoin.New(bitcoin.Config{MaxTransactionBytes: 10})
	tx, _ := signed(t)
	if _, err := s.Encode(tx); err == nil {
		t.Fatal("encode budget ignored")
	}
	if _, err := s.Decode(make([]byte, 11)); err == nil {
		t.Fatal("decode budget ignored")
	}
	if _, err := s.Encode(nil); err == nil {
		t.Fatal("nil transaction accepted")
	}
	s, _ = bitcoin.New(bitcoin.Config{})
	tx.TxIn[0] = nil
	if _, err := s.Encode(tx); err == nil {
		t.Fatal("nil input accepted")
	}
	if _, err := bitcoin.New(bitcoin.Config{MaxTransactionBytes: -1}); err == nil {
		t.Fatal("invalid configuration accepted")
	}
}

func TestWitnessRoundTripAndAmountBinding(t *testing.T) {
	s, _ := bitcoin.New(bitcoin.Config{})
	key, _ := btcec.PrivKeyFromBytes(bytes.Repeat([]byte{2}, 32))
	script, err := txscript.NewScriptBuilder().AddData(key.PubKey().SerializeCompressed()).AddOp(txscript.OP_CHECKSIG).Script()
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(script)
	program, err := txscript.NewScriptBuilder().AddOp(txscript.OP_0).AddData(hash[:]).Script()
	if err != nil {
		t.Fatal(err)
	}
	point := wire.OutPoint{Index: 1}
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(wire.NewTxIn(&point, nil, nil))
	tx.AddTxOut(wire.NewTxOut(900, []byte{txscript.OP_TRUE}))
	prev := map[wire.OutPoint]*wire.TxOut{point: wire.NewTxOut(1000, program)}
	fetcher := txscript.NewMultiPrevOutFetcher(prev)
	sig, err := txscript.RawTxInWitnessSignature(tx, txscript.NewTxSigHashes(tx, fetcher), 0, 1000, script, txscript.SigHashAll, key)
	if err != nil {
		t.Fatal(err)
	}
	tx.TxIn[0].Witness = wire.TxWitness{sig, script}
	raw, err := s.Encode(tx)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := s.Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.Encode(decoded)
	if err != nil || !bytes.Equal(raw, again) || decoded.WitnessHash() != tx.WitnessHash() {
		t.Fatal("witness encoding changed")
	}
	if err = s.VerifyScripts(context.Background(), decoded, prev); err != nil {
		t.Fatal(err)
	}
	prev[point].Value++
	if err = s.VerifyScripts(context.Background(), decoded, prev); err == nil {
		t.Fatal("witness signature accepted incorrect prevout amount")
	}
}
