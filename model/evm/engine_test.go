package evm_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	geth "github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/crypto"
	modelcap "github.com/lengzhao/GoChainKit/cap/model"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/GoChainKit/internal/modeltest"
	model "github.com/lengzhao/GoChainKit/model/evm"
)

type block core.ID

func (b block) ID() core.ID { return core.ID(b) }

type execution struct{ height uint64 }

func (e execution) Height() uint64                 { return e.height }
func (e execution) Timestamp() int64               { return int64(1700000000 + e.height) }
func (e execution) Parent() core.Block             { return block(core.ID(string(bytes.Repeat([]byte{1}, 32)))) }
func (e execution) Candidate() core.Block          { return block("candidate") }
func (e execution) History() modelcap.BlockHistory { return nil }
func (e execution) BlockHash(context.Context, uint64) (core.ID, error) {
	return core.ID(string(bytes.Repeat([]byte{1}, 32))), nil
}

func setup(t *testing.T) (*model.Engine, map[string][]byte, common.Address) {
	t.Helper()
	key, _ := crypto.ToECDSA(bytes.Repeat([]byte{1}, 32))
	from := crypto.PubkeyToAddress(key.PublicKey)
	e, err := model.New(model.Config{Network: "evm-test", ChainID: "1337", GasLimit: 1000000, BaseFee: "1", Allocations: map[string]model.Account{from.Hex(): {Balance: "1000000000000"}}})
	if err != nil {
		t.Fatal(err)
	}
	genesis, err := e.Genesis(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return e, genesis, from
}
func signed(t *testing.T, e *model.Engine, nonce uint64, to *common.Address, data []byte, gas uint64, value int64) core.Transaction {
	t.Helper()
	key, _ := crypto.ToECDSA(bytes.Repeat([]byte{1}, 32))
	tx, err := types.SignTx(types.NewTx(&types.DynamicFeeTx{ChainID: big.NewInt(1337), Nonce: nonce, To: to, Data: data, Gas: gas, GasFeeCap: big.NewInt(2), GasTipCap: big.NewInt(1), Value: big.NewInt(value)}), types.NewLondonSigner(big.NewInt(1337)), key)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := tx.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	wrapped, err := e.DecodeTransaction(raw)
	if err != nil {
		t.Fatal(err)
	}
	frozen, err := e.EncodeTransaction(wrapped)
	if err != nil || !bytes.Equal(frozen, raw) {
		t.Fatal("native encoding changed")
	}
	raw[0] ^= 255
	if wrapped.ID() != core.ID(string(tx.Hash().Bytes())) {
		t.Fatal("mutable transaction ID")
	}
	return wrapped
}
func apply(t *testing.T, e *model.Engine, state map[string][]byte, txs []core.Transaction, height uint64) (map[string][]byte, modelcap.Transition) {
	t.Helper()
	tr, err := e.ExecuteInContext(context.Background(), modeltest.Reader(state), txs, execution{height})
	if err != nil {
		t.Fatal(err)
	}
	next, err := core.ApplyStateChanges(context.Background(), state, tr.Writes, tr.Deletes)
	if err != nil {
		t.Fatal(err)
	}
	return next, tr
}
func TestTransferGasNonceAndIsolation(t *testing.T) {
	e, genesis, from := setup(t)
	to := common.HexToAddress("0x1000000000000000000000000000000000000001")
	tx := signed(t, e, 0, &to, nil, 21000, 123)
	next, tr := apply(t, e, genesis, []core.Transaction{tx}, 1)
	account, err := e.Account(context.Background(), modeltest.Reader(next), to)
	if err != nil || account.Balance != "123" {
		t.Fatalf("recipient %+v %v", account, err)
	}
	sender, err := e.Account(context.Background(), modeltest.Reader(next), from)
	if err != nil || sender.Nonce != 1 || sender.Balance != "999999957877" {
		t.Fatalf("sender %+v %v", sender, err)
	}
	old, err := e.Account(context.Background(), modeltest.Reader(genesis), from)
	if err != nil || old.Nonce != 0 || old.Balance != "1000000000000" {
		t.Fatal("parent mutated")
	}
	var receipt types.Receipt
	if err := json.Unmarshal(tr.Receipts[0].Data, &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.Status != 1 || receipt.GasUsed != 21000 || receipt.TxHash != common.BytesToHash([]byte(tx.ID())) {
		t.Fatalf("receipt %+v", receipt)
	}
	if _, err := e.ExecuteInContext(context.Background(), modeltest.Reader(next), []core.Transaction{tx}, execution{2}); !errors.Is(err, core.ErrInvalidTransaction) {
		t.Fatalf("duplicate nonce: %v", err)
	}
	poor := signed(t, e, 1, &to, nil, 21000, 1000000000001)
	if _, err := e.ExecuteInContext(context.Background(), modeltest.Reader(next), []core.Transaction{poor}, execution{2}); !errors.Is(err, core.ErrDeferredTransaction) {
		t.Fatalf("insufficient balance: %v", err)
	}
}
func TestDeployCallStorageAndRevert(t *testing.T) {
	e, genesis, from := setup(t)
	runtime, _ := hex.DecodeString("602a60005560005460005260206000f3")
	prefix := []byte{0x60, byte(len(runtime)), 0x60, 12, 0x60, 0, 0x39, 0x60, byte(len(runtime)), 0x60, 0, 0xf3}
	deploy := signed(t, e, 0, nil, append(prefix, runtime...), 150000, 0)
	state, _ := apply(t, e, genesis, []core.Transaction{deploy}, 1)
	address := crypto.CreateAddress(from, 0)
	info, err := e.Account(context.Background(), modeltest.Reader(state), address)
	if err != nil || !bytes.Equal(info.Code, runtime) {
		t.Fatalf("code %+v %v", info, err)
	}
	output, err := e.Call(context.Background(), modeltest.Reader(state), model.CallRequest{From: from, To: address, Gas: 100000})
	if err != nil || new(big.Int).SetBytes(output.Data).Uint64() != 42 {
		t.Fatalf("call %+v %v", output, err)
	}
	untouched, err := e.Storage(context.Background(), modeltest.Reader(state), address, common.Hash{})
	if err != nil || untouched != (common.Hash{}) {
		t.Fatal("read-only call persisted storage")
	}
	state, tr := apply(t, e, state, []core.Transaction{signed(t, e, 1, &address, nil, 100000, 0)}, 2)
	slot, err := e.Storage(context.Background(), modeltest.Reader(state), address, common.Hash{})
	if err != nil || slot != common.BigToHash(big.NewInt(42)) {
		t.Fatalf("storage %v %v", slot, err)
	}
	var receipt types.Receipt
	if err := json.Unmarshal(tr.Receipts[0].Data, &receipt); err != nil || receipt.Status != 1 {
		t.Fatalf("receipt %v %v", receipt.Status, err)
	}
	// Runtime writes a slot then REVERTs. The transaction remains valid and consumes gas/nonce.
	revertRuntime, _ := hex.DecodeString("602b60005560006000fd")
	e2, err := model.New(model.Config{Network: "revert", ChainID: "1337", BaseFee: "1", Allocations: map[string]model.Account{from.Hex(): {Balance: "1000000000"}, address.Hex(): {Code: revertRuntime, Balance: "0"}}})
	if err != nil {
		t.Fatal(err)
	}
	gs, err := e2.Genesis(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, callErr := e2.Call(context.Background(), modeltest.Reader(gs), model.CallRequest{From: from, To: address, Gas: 100000})
	if !errors.Is(callErr, model.ErrCallExecution) || !errors.Is(callErr, vm.ErrExecutionReverted) {
		t.Fatalf("revert call error not preserved/classified: %v", callErr)
	}
	ns, tr := apply(t, e2, gs, []core.Transaction{signed(t, e2, 0, &address, nil, 100000, 0)}, 1)
	if err := json.Unmarshal(tr.Receipts[0].Data, &receipt); err != nil || receipt.Status != 0 || receipt.GasUsed <= 21000 {
		t.Fatalf("revert receipt %+v %v", receipt, err)
	}
	slot, err = e2.Storage(context.Background(), modeltest.Reader(ns), address, common.Hash{})
	if err != nil || slot != (common.Hash{}) {
		t.Fatal("revert storage persisted")
	}
	sender, err := e2.Account(context.Background(), modeltest.Reader(ns), from)
	if err != nil || sender.Nonce != 1 {
		t.Fatal("revert did not consume nonce")
	}
}
func TestConfigurationAndCancellation(t *testing.T) {
	for _, c := range []model.Config{{}, {Network: "x", ChainID: "0"}, {Network: "x", ChainID: "1", BaseFee: "-1"}, {Network: "x", ChainID: "1", Allocations: map[string]model.Account{"bad": {Balance: "1"}}}} {
		if _, err := model.New(c); err == nil {
			t.Fatalf("invalid config %+v", c)
		}
	}
	e, gs, _ := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.ExecuteInContext(ctx, modeltest.Reader(gs), nil, execution{1}); err != context.Canceled {
		t.Fatalf("cancel %v", err)
	}
	if _, err := e.Execute(context.Background(), modeltest.Reader(gs), nil); err == nil {
		t.Fatal("missing context accepted")
	}
}

func TestNilExecutionTransaction(t *testing.T) {
	e, gs, _ := setup(t)
	if _, err := e.ExecuteInContext(context.Background(), modeltest.Reader(gs), []core.Transaction{nil}, execution{1}); !errors.Is(err, core.ErrInvalidTransaction) {
		t.Fatalf("nil transaction: %v", err)
	}
}

type brokenHistory struct{ execution }

var historyFailure = errors.New("history database failure")

func (b brokenHistory) BlockHash(context.Context, uint64) (core.ID, error) { return "", historyFailure }

func TestBlockHashContextAndStorageRestoration(t *testing.T) {
	key, _ := crypto.ToECDSA(bytes.Repeat([]byte{1}, 32))
	from := crypto.PubkeyToAddress(key.PublicKey)
	address := common.HexToAddress("0x2000000000000000000000000000000000000002")
	code, _ := hex.DecodeString("60014060005260206000f3")
	word := common.BigToHash(big.NewInt(7))
	e, err := model.New(model.Config{Network: "history", ChainID: "1337", Allocations: map[string]model.Account{from.Hex(): {Balance: "1000000000"}, address.Hex(): {Balance: "0", Code: code, Storage: map[string]string{common.Hash{}.Hex(): word.Hex()}}}})
	if err != nil {
		t.Fatal(err)
	}
	genesis, err := e.Genesis(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	state, _ := apply(t, e, genesis, nil, 1)
	slot, err := e.Storage(context.Background(), modeltest.Reader(state), address, common.Hash{})
	if err != nil || slot != word {
		t.Fatalf("restored slot %v %v", slot, err)
	}
	tx := signed(t, e, 0, &address, nil, 100000, 0)
	if _, err := e.ExecuteInContext(context.Background(), modeltest.Reader(state), []core.Transaction{tx}, brokenHistory{execution{2}}); !errors.Is(err, historyFailure) || errors.Is(err, core.ErrInvalidTransaction) {
		t.Fatalf("history error masked: %v", err)
	}
	state, _ = apply(t, e, state, []core.Transaction{tx}, 2)
	output, err := e.Call(context.Background(), modeltest.Reader(state), model.CallRequest{From: from, To: address, Gas: 100000})
	if err != nil || !bytes.Equal(output.Data, bytes.Repeat([]byte{1}, 32)) {
		t.Fatalf("historical hash %x %v", output.Data, err)
	}
}

func TestRunningEVMHonorsCancellation(t *testing.T) {
	address := common.HexToAddress("0x3000000000000000000000000000000000000003")
	e, err := model.New(model.Config{Network: "cancel", ChainID: "1337", GasLimit: 100000000, Allocations: map[string]model.Account{address.Hex(): {Balance: "0", Code: []byte{0x5b, 0x60, 0, 0x56}}}})
	if err != nil {
		t.Fatal(err)
	}
	gs, err := e.Genesis(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	_, err = e.Call(ctx, modeltest.Reader(gs), model.CallRequest{To: address, Gas: 100000000})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("running EVM cancellation: %v", err)
	}
}

func TestGenesisPreservesStorageOnlyAllocation(t *testing.T) {
	address := common.HexToAddress("0x4000000000000000000000000000000000000004")
	value := common.BigToHash(big.NewInt(9))
	e, err := model.New(model.Config{Network: "genesis-storage", ChainID: "1337", Allocations: map[string]model.Account{address.Hex(): {Balance: "0", Storage: map[string]string{common.Hash{}.Hex(): value.Hex()}}}})
	if err != nil {
		t.Fatal(err)
	}
	gs, err := e.Genesis(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got, err := e.Storage(context.Background(), modeltest.Reader(gs), address, common.Hash{})
	if err != nil || got != value {
		t.Fatalf("genesis storage: %v %v", got, err)
	}
}

func TestCallInContextUsesAnchoredBlockEnvironment(t *testing.T) {
	address := common.HexToAddress("0x5000000000000000000000000000000000000005")
	code, _ := hex.DecodeString("436000524260205260406000f3")
	e, err := model.New(model.Config{Network: "call-context", ChainID: "1337", Allocations: map[string]model.Account{address.Hex(): {Balance: "0", Code: code}}})
	if err != nil {
		t.Fatal(err)
	}
	gs, err := e.Genesis(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	output, err := e.CallInContext(context.Background(), modeltest.Reader(gs), model.CallRequest{To: address, Gas: 100000}, execution{0})
	if err != nil || len(output.Data) != 64 {
		t.Fatalf("call context: %x %v", output.Data, err)
	}
	if new(big.Int).SetBytes(output.Data[:32]).Uint64() != 0 || new(big.Int).SetBytes(output.Data[32:]).Uint64() != 1700000000 {
		t.Fatalf("wrong anchored environment %x", output.Data)
	}
	if _, err = e.CallInContext(context.Background(), modeltest.Reader(gs), model.CallRequest{To: address, Gas: 100000}, execution{1}); err == nil {
		t.Fatal("foreign snapshot height accepted")
	}
}

func TestCallInputErrorsAreClassified(t *testing.T) {
	e, gs, _ := setup(t)
	for _, request := range []model.CallRequest{{Value: "wrong"}, {Value: "-1"}, {Value: new(big.Int).Lsh(big.NewInt(1), 256).String()}, {Gas: 1000001}, {Data: make([]byte, (1<<20)+1)}} {
		if _, err := e.CallInContext(context.Background(), modeltest.Reader(gs), request, execution{0}); !errors.Is(err, model.ErrCallInput) {
			t.Fatalf("input error not classified: %v", err)
		}
	}
	if _, err := e.CallInContext(context.Background(), modeltest.Reader(gs), model.CallRequest{}, nil); errors.Is(err, model.ErrCallInput) {
		t.Fatal("internal context failure reported as caller input")
	}
}

func TestCallExecutionAndIntrinsicGasErrors(t *testing.T) {
	address := common.HexToAddress("0x6000000000000000000000000000000000000006")
	e, err := model.New(model.Config{Network: "call-errors", ChainID: "1337", Allocations: map[string]model.Account{address.Hex(): {Balance: "0", Code: []byte{0xfe}}}})
	if err != nil {
		t.Fatal(err)
	}
	gs, err := e.Genesis(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.CallInContext(context.Background(), modeltest.Reader(gs), model.CallRequest{To: address, Gas: 100000}, execution{0})
	var opcode *vm.ErrInvalidOpCode
	if !errors.Is(err, model.ErrCallExecution) || !errors.As(err, &opcode) {
		t.Fatalf("invalid opcode error not preserved/classified: %v", err)
	}
	_, err = e.CallInContext(context.Background(), modeltest.Reader(gs), model.CallRequest{To: address, Gas: 1}, execution{0})
	if !errors.Is(err, model.ErrCallInput) || !errors.Is(err, geth.ErrIntrinsicGas) {
		t.Fatalf("intrinsic gas error not preserved/classified: %v", err)
	}
}
