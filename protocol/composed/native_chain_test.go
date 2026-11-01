package composed_test

import (
	"bytes"
	"context"
	"errors"
	"math/big"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/txscript/v2"
	"github.com/btcsuite/btcd/wire/v2"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/lengzhao/GoChainKit/cap/model"
	"github.com/lengzhao/GoChainKit/core"
	btc "github.com/lengzhao/GoChainKit/model/bitcoin"
	"github.com/lengzhao/GoChainKit/model/evm"
	"github.com/lengzhao/GoChainKit/protocol/composed"
	pow "github.com/lengzhao/GoChainKit/rules/pow"
)

type nativeFixture struct {
	engine model.Engine
	tx     core.Transaction
	check  func(*testing.T, core.StateReader, bool)
}

func nativeFixtureFor(t *testing.T, kind string) nativeFixture {
	t.Helper()
	ctx := context.Background()
	if kind == "bitcoin" {
		key, _ := btcec.PrivKeyFromBytes(bytes.Repeat([]byte{1}, 32))
		script, err := txscript.NewScriptBuilder().AddData(key.PubKey().SerializeCompressed()).AddOp(txscript.OP_CHECKSIG).Script()
		if err != nil {
			t.Fatal(err)
		}
		e, err := btc.New(btc.Config{Network: "native-bitcoin", GenesisTime: 1700000000, Allocations: []btc.Allocation{{Value: 100000, Script: script}}})
		if err != nil {
			t.Fatal(err)
		}
		point := e.GenesisOutpoints()[0]
		raw := wire.NewMsgTx(2)
		raw.AddTxIn(wire.NewTxIn(&point, nil, nil))
		raw.AddTxOut(wire.NewTxOut(90000, script))
		sig, err := txscript.RawTxInSignature(raw, 0, script, txscript.SigHashAll, key)
		if err != nil {
			t.Fatal(err)
		}
		raw.TxIn[0].SignatureScript, err = txscript.NewScriptBuilder().AddData(sig).Script()
		if err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		if err = raw.Serialize(&buf); err != nil {
			t.Fatal(err)
		}
		tx, err := e.DecodeTransaction(buf.Bytes())
		if err != nil {
			t.Fatal(err)
		}
		return nativeFixture{e, tx, func(t *testing.T, s core.StateReader, applied bool) {
			t.Helper()
			o, err := e.Output(ctx, s, point)
			if applied {
				if !errors.Is(err, core.ErrNotFound) {
					t.Fatalf("spent genesis remains: %v", err)
				}
				coin, err := e.Output(ctx, s, wire.OutPoint{Hash: raw.TxHash(), Index: 0})
				if err != nil || coin.Value != 90000 {
					t.Fatalf("created output: %+v %v", coin, err)
				}
			} else if err != nil || o.Value != 100000 {
				t.Fatalf("genesis not restored: %+v %v", o, err)
			}
		}}
	}
	key, err := crypto.ToECDSA(bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal(err)
	}
	owner := crypto.PubkeyToAddress(key.PublicKey)
	recipient := common.Address{2}
	e, err := evm.New(evm.Config{Network: "native-evm", ChainID: "1337", BaseFee: "1", GasLimit: 1000000, Allocations: map[string]evm.Account{owner.Hex(): {Balance: "1000000000000000000"}}})
	if err != nil {
		t.Fatal(err)
	}
	inner := &types.LegacyTx{To: &recipient, Value: big.NewInt(7), Gas: 21000, GasPrice: big.NewInt(2)}
	if kind == "evm-contract" {
		inner.To = nil
		inner.Value = big.NewInt(0)
		inner.Gas = 150000
		inner.Data = []byte{0x60, 0x2a, 0x60, 0, 0x55, 0x60, 0, 0x60, 0, 0xf3}
	}
	raw, err := types.SignTx(types.NewTx(inner), types.LatestSignerForChainID(big.NewInt(1337)), key)
	if err != nil {
		t.Fatal(err)
	}
	data, err := raw.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	tx, err := e.DecodeTransaction(data)
	if err != nil {
		t.Fatal(err)
	}
	return nativeFixture{e, tx, func(t *testing.T, s core.StateReader, applied bool) {
		t.Helper()
		a, err := e.Account(ctx, s, recipient)
		if err != nil {
			t.Fatal(err)
		}
		want := "0"
		nonce := uint64(0)
		if applied {
			want = "7"
			nonce = 1
		}
		if kind == "evm-contract" {
			address := crypto.CreateAddress(owner, 0)
			slot, err := e.Storage(ctx, s, address, common.Hash{})
			if err != nil {
				t.Fatal(err)
			}
			expected := common.Hash{}
			if applied {
				expected[31] = 42
			}
			if slot != expected {
				t.Fatalf("contract storage %s want %s", slot.Hex(), expected.Hex())
			}
		} else if a.Balance != want {
			t.Fatalf("recipient balance %s want %s", a.Balance, want)
		}
		a, err = e.Account(ctx, s, owner)
		if err != nil || a.Nonce != nonce {
			t.Fatalf("owner nonce %d want %d: %v", a.Nonce, nonce, err)
		}
	}}
}

func TestNativeChainsReorgNetworkAndRecovery(t *testing.T) {
	for _, kind := range []string{"bitcoin", "evm", "evm-contract"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			f := nativeFixtureFor(t, kind)
			clock := func() time.Time { return time.Unix(1700000010, 0) }
			rule, err := pow.New(pow.Config{RetargetInterval: 100, TargetSpacing: 10, Clock: clock})
			if err != nil {
				t.Fatal(err)
			}
			p, err := composed.NewPoW(composed.Config{Network: f.engine.Protocol().Network, GenesisTime: 1700000000, Clock: clock}, f.engine, rule, rule)
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			aPath, bPath := filepath.Join(dir, "a"), filepath.Join(dir, "b")
			a, b := startNode(t, p, aPath), startNode(t, p, bPath)
			inspect := func(n *composedNode, applied bool) {
				t.Helper()
				head, err := n.ledger.Head(ctx)
				if err != nil {
					t.Fatal(err)
				}
				s, err := n.ledger.Snapshot(ctx, head)
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close()
				f.check(t, s, applied)
			}
			mine := func(n *composedNode, parent core.ID) core.Block {
				t.Helper()
				block, r, err := n.builder.Build(ctx, parent)
				if err != nil {
					t.Fatal(err)
				}
				block, err = p.Seal(ctx, block)
				if err != nil {
					t.Fatal(err)
				}
				r.BlockID = block.ID()
				if _, err = n.ledger.Import(ctx, parent, block, r); err != nil {
					t.Fatal(err)
				}
				txs, err := p.Transactions(block)
				if err != nil {
					t.Fatal(err)
				}
				ids := make([]core.ID, len(txs))
				for i, tx := range txs {
					ids[i] = tx.ID()
				}
				if err = n.pool.Remove(ctx, ids); err != nil {
					t.Fatal(err)
				}
				return block
			}
			genesis, err := a.ledger.Head(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if err = a.pool.Add(ctx, f.tx); err != nil {
				t.Fatal(err)
			}
			first := mine(a, genesis)
			inspect(a, true)
			side := mine(b, genesis)
			winner := mine(b, side.ID())
			inspect(b, false)
			if err = a.network.Connect(ctx, b.network.Addresses()[0]); err != nil {
				t.Fatal(err)
			}
			eventually(t, func() bool { head, _ := a.ledger.Head(ctx); return head == winner.ID() })
			inspect(a, false)
			if _, err = a.ledger.Transaction(ctx, f.tx.ID()); !errors.Is(err, core.ErrNotFound) {
				t.Fatalf("detached transaction canonical: %v", err)
			}
			historical, err := a.ledger.Snapshot(ctx, first.ID())
			if err != nil {
				t.Fatal(err)
			}
			f.check(t, historical, true)
			historical.Close()
			b.stop(t)
			if err = a.pool.Add(ctx, f.tx); err != nil {
				t.Fatal(err)
			}
			confirmed := mine(a, winner.ID())
			tip := mine(a, confirmed.ID())
			inspect(a, true)
			b = startNode(t, p, bPath)
			if err = b.network.Connect(ctx, a.network.Addresses()[0]); err != nil {
				t.Fatal(err)
			}
			eventually(t, func() bool { head, _ := b.ledger.Head(ctx); return head == tip.ID() })
			inspect(b, true)
			for _, n := range []*composedNode{a, b} {
				if tx, err := n.ledger.Transaction(ctx, f.tx.ID()); err != nil || tx.BlockID != confirmed.ID() {
					t.Fatalf("confirmed transaction missing: %v", err)
				}
				if r, err := n.ledger.Receipt(ctx, f.tx.ID()); err != nil || len(r.Data) == 0 {
					t.Fatalf("receipt missing: %v", err)
				}
			}
			sa, err := a.ledger.Snapshot(ctx, tip.ID())
			if err != nil {
				t.Fatal(err)
			}
			sb, err := b.ledger.Snapshot(ctx, tip.ID())
			if err != nil {
				t.Fatal(err)
			}
			ea, err := sa.Entries(ctx)
			if err != nil {
				t.Fatal(err)
			}
			eb, err := sb.Entries(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if sa.Root() != sb.Root() || !reflect.DeepEqual(ea, eb) {
				t.Fatal("replay state divergence")
			}
			sa.Close()
			sb.Close()
			a.stop(t)
			a = startNode(t, p, aPath)
			inspect(a, true)
		})
	}
}
