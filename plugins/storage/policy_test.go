package storage

import (
	"context"
	"fmt"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/GoChainKit/internal/testprotocol"
	"math/big"
	"path/filepath"
	"testing"
)

type linearFormat struct{ *testprotocol.Format }

func (p linearFormat) Parent(b core.Block) (core.ID, error) {
	return b.(testprotocol.Frame).Ancestor, nil
}

type heightPolicy struct {
	p     linearFormat
	final bool
	name  string
}

func (p heightPolicy) Protocol() core.ProtocolDescriptor { return p.p.Protocol() }
func (p heightPolicy) PolicyID() string                  { return "test-height/v1/" + p.name + fmt.Sprint(p.final) }
func (p heightPolicy) Score(_ context.Context, _ core.ChainStore, b core.Block, _ []byte) ([]byte, error) {
	return new(big.Int).SetUint64(b.(testprotocol.Frame).Revision).Bytes(), nil
}
func (p heightPolicy) Prefer(_ context.Context, _ core.ChainStore, _, _ core.Block, a, b []byte) (bool, error) {
	return new(big.Int).SetBytes(b).Cmp(new(big.Int).SetBytes(a)) > 0, nil
}
func (p heightPolicy) Finalized(_ context.Context, _ core.ChainStore, b core.Block) (core.ID, error) {
	if p.final {
		return b.ID(), nil
	}
	return "", nil
}
func TestNonWorkPolicyAndFinality(t *testing.T) {
	for _, disk := range []bool{false, true} {
		t.Run(fmt.Sprint(disk), func(t *testing.T) {
			ctx := context.Background()
			p := linearFormat{testprotocol.New("policy")}
			policy := heightPolicy{p: p, final: true}
			path := filepath.Join(t.TempDir(), "policy.db")
			var s core.BranchLedger
			if disk {
				v, e := NewBolt(BoltConfig{Path: path}, Deps{Protocol: p, Policy: policy})
				if e != nil {
					t.Fatal(e)
				}
				if e = v.Start(ctx); e != nil {
					t.Fatal(e)
				}
				defer v.Stop(ctx)
				s = v
			} else {
				v, e := NewMemory(MemoryConfig{}, Deps{Protocol: p, Policy: policy})
				if e != nil {
					t.Fatal(e)
				}
				s = v
			}
			g, _ := s.Head(ctx)
			build := func(parent core.ID, label string) (core.Block, core.ExecutionResult) {
				pb, _ := s.Block(ctx, parent)
				d, _ := p.Draft(ctx, pb, []core.Transaction{testprotocol.Tx{Label: label}})
				snap, _ := s.Snapshot(ctx, parent)
				r, e := (testprotocol.Executor{P: p}).Execute(ctx, snap, d)
				snap.Close()
				if e != nil {
					t.Fatal(e)
				}
				b, _ := p.Finalize(ctx, d, r)
				r.BlockID = b.ID()
				return b, r
			}
			a, ar := build(g, "a")
			if _, e := s.Import(ctx, g, a, ar); e != nil {
				t.Fatal(e)
			}
			b, br := build(g, "b")
			if _, e := s.Import(ctx, g, b, br); e != nil {
				t.Fatal("valid side branch must save", e)
			}
			c, cr := build(b.ID(), "c")
			if _, e := s.Import(ctx, b.ID(), c, cr); e != nil {
				t.Fatal(e)
			}
			head, _ := s.Head(ctx)
			if head != a.ID() {
				t.Fatal("reorganized across finalized block")
			}
			if disk {
				s.(*BoltStore).Stop(ctx)
				v, e := NewBolt(BoltConfig{Path: path}, Deps{Protocol: p, Policy: policy})
				if e != nil {
					t.Fatal(e)
				}
				if e = v.Start(ctx); e != nil {
					t.Fatal(e)
				}
				v.Stop(ctx)
				wrong, _ := NewBolt(BoltConfig{Path: path}, Deps{Protocol: p, Policy: heightPolicy{p: p, final: true, name: "changed"}})
				if e := wrong.Start(ctx); e == nil {
					wrong.Stop(ctx)
					t.Fatal("changed policy opened database")
				}
			}
		})
	}
}

type delayedFinalityPolicy struct{ heightPolicy }

func (p delayedFinalityPolicy) PolicyID() string { return "delayed-finality/v1" }
func (p delayedFinalityPolicy) Finalized(ctx context.Context, chain core.ChainStore, b core.Block) (core.ID, error) {
	f := b.(testprotocol.Frame)
	if len(f.Records) == 0 || f.Records[0].Label != "certify" {
		return "", nil
	}
	parent, e := chain.Block(ctx, f.Ancestor)
	if e != nil {
		return "", e
	}
	return parent.(testprotocol.Frame).Ancestor, nil
}
func TestCanonicalFinalityCannotBeForgottenAcrossSwitch(t *testing.T) {
	for _, disk := range []bool{false, true} {
		t.Run(fmt.Sprint(disk), func(t *testing.T) {
			ctx := context.Background()
			p := linearFormat{testprotocol.New("delayed-finality")}
			policy := delayedFinalityPolicy{heightPolicy{p: p}}
			path := filepath.Join(t.TempDir(), "finality.db")
			var s core.BranchLedger
			open := func() core.BranchLedger {
				if !disk {
					v, e := NewMemory(MemoryConfig{}, Deps{Protocol: p, Policy: policy})
					if e != nil {
						t.Fatal(e)
					}
					return v
				}
				v, e := NewBolt(BoltConfig{Path: path}, Deps{Protocol: p, Policy: policy})
				if e != nil {
					t.Fatal(e)
				}
				if e = v.Start(ctx); e != nil {
					t.Fatal(e)
				}
				return v
			}
			s = open()
			defer func() {
				if v, ok := s.(*BoltStore); ok {
					v.Stop(ctx)
				}
			}()
			build := func(parent core.ID, label string) core.ID {
				pb, _ := s.Block(ctx, parent)
				d, _ := p.Draft(ctx, pb, []core.Transaction{testprotocol.Tx{Label: label}})
				snap, _ := s.Snapshot(ctx, parent)
				r, e := (testprotocol.Executor{P: p}).Execute(ctx, snap, d)
				snap.Close()
				if e != nil {
					t.Fatal(e)
				}
				b, _ := p.Finalize(ctx, d, r)
				r.BlockID = b.ID()
				if _, e = s.Import(ctx, parent, b, r); e != nil {
					t.Fatal(e)
				}
				return b.ID()
			}
			g, _ := s.Head(ctx)
			a := build(g, "a")
			b := build(a, "b")
			c := build(b, "certify")
			d := build(a, "d")
			e := build(d, "e")
			_ = build(e, "f")
			head, _ := s.Head(ctx)
			if head != c {
				t.Fatal("candidate forgot canonical finalized ancestor")
			}
			if disk {
				s.(*BoltStore).Stop(ctx)
				s = open()
			}
			h := build(g, "h")
			i := build(h, "i")
			j := build(i, "j")
			_ = build(j, "k")
			head, _ = s.Head(ctx)
			if head != c {
				t.Fatal("two-step switch crossed prior finality")
			}
		})
	}
}
