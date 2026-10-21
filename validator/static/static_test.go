package static_test

import (
	"context"
	"github.com/lengzhao/GoChainKit/cap/signer"
	cap "github.com/lengzhao/GoChainKit/cap/validator"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/GoChainKit/validator/static"
	"testing"
)

type block core.ID

func (b block) ID() core.ID { return core.ID(b) }
func TestFrozenOrderedMembership(t *testing.T) {
	members := []cap.Member{{ID: "a", Key: signer.PublicKey{Algorithm: "external-suite/v1", Bytes: []byte{1}}}, {ID: "b", Key: signer.PublicKey{Algorithm: "external-suite/v1", Bytes: []byte{2}}}}
	p, e := static.New(static.Config{Members: members})
	if e != nil {
		t.Fatal(e)
	}
	q, e := static.New(static.Config{Members: []cap.Member{members[1], members[0]}})
	if e != nil || q.Identity() == p.Identity() {
		t.Fatal("order unbound")
	}
	members[0].Key.Bytes[0] = 8
	set, e := p.Members(context.Background(), block("parent"))
	if e != nil || set.Anchor != "parent" || set.ID != p.Identity() || set.Members[0].Key.Bytes[0] != 1 {
		t.Fatal(set, e)
	}
	set.Members[0].Key.Bytes[0] = 9
	again, _ := p.Members(context.Background(), block("another"))
	if again.Members[0].Key.Bytes[0] != 1 || again.Anchor != "another" {
		t.Fatal("alias")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e = p.Members(ctx, block("p")); e == nil {
		t.Fatal("cancel")
	}
	if _, e = p.Members(context.Background(), nil); e == nil {
		t.Fatal("nil parent")
	}
	for _, ms := range [][]cap.Member{nil, {{ID: "x"}}, {{ID: "a", Key: signer.PublicKey{Algorithm: "x", Bytes: []byte{1}}}, {ID: "a", Key: signer.PublicKey{Algorithm: "y", Bytes: []byte{2}}}}, {{ID: "a", Key: signer.PublicKey{Algorithm: "x", Bytes: []byte{1}}}, {ID: "b", Key: signer.PublicKey{Algorithm: "x", Bytes: []byte{1}}}}} {
		if _, e = static.New(static.Config{Members: ms}); e == nil {
			t.Fatal("bad members accepted")
		}
	}
}

func TestIdentityCommitsEachMembershipDimension(t *testing.T) {
	base := []cap.Member{{ID: "a", Key: signer.PublicKey{Algorithm: "suite/v1", Bytes: []byte{1}}}, {ID: "b", Key: signer.PublicKey{Algorithm: "suite/v1", Bytes: []byte{2}}}}
	original, e := static.New(static.Config{Members: base})
	if e != nil {
		t.Fatal(e)
	}
	for _, dimension := range []string{"id", "suite", "key", "order", "count"} {
		t.Run(dimension, func(t *testing.T) {
			members := make([]cap.Member, len(base))
			copy(members, base)
			switch dimension {
			case "id":
				members[0].ID = "renamed"
			case "suite":
				members[0].Key.Algorithm = "suite/v2"
			case "key":
				members[0].Key.Bytes = []byte{3}
			case "order":
				members[0], members[1] = members[1], members[0]
			case "count":
				members = members[:1]
			}
			changed, e := static.New(static.Config{Members: members})
			if e != nil {
				t.Fatal(e)
			}
			if changed.Identity() == original.Identity() {
				t.Fatal("membership change did not alter identity")
			}
		})
	}
	identical, e := static.New(static.Config{Members: base})
	if e != nil || identical.Identity() != original.Identity() {
		t.Fatal("same membership identity is unstable", e)
	}
}
