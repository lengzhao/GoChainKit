// Package static provides a frozen, ordered consensus membership configuration.
package static

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	cap "github.com/lengzhao/GoChainKit/cap/validator"
	"github.com/lengzhao/GoChainKit/core"
)

type Config struct {
	Members []cap.Member `json:"members"`
}
type Provider struct {
	members []cap.Member
	id      core.ID
}

func clone(ms []cap.Member) []cap.Member {
	out := make([]cap.Member, len(ms))
	for i, m := range ms {
		out[i] = m
		out[i].Key.Bytes = bytes.Clone(m.Key.Bytes)
	}
	return out
}
func field(b, v []byte) []byte {
	b = binary.BigEndian.AppendUint64(b, uint64(len(v)))
	return append(b, v...)
}
func New(c Config) (*Provider, error) {
	if len(c.Members) == 0 {
		return nil, fmt.Errorf("static validators: empty set")
	}
	ids := map[core.ID]bool{}
	keys := map[string]bool{}
	b := []byte("GoChainKit/validators/static/v1\x00")
	b = binary.BigEndian.AppendUint64(b, uint64(len(c.Members)))
	for _, m := range c.Members {
		if m.ID == "" || m.Key.Algorithm == "" || len(m.Key.Bytes) == 0 || ids[m.ID] {
			return nil, fmt.Errorf("static validators: invalid or duplicate member")
		}
		k := field(nil, []byte(m.Key.Algorithm))
		k = field(k, m.Key.Bytes)
		if keys[string(k)] {
			return nil, fmt.Errorf("static validators: duplicate public key")
		}
		ids[m.ID] = true
		keys[string(k)] = true
		b = field(b, []byte(m.ID))
		b = field(b, []byte(m.Key.Algorithm))
		b = field(b, m.Key.Bytes)
	}
	h := sha256.Sum256(b)
	return &Provider{members: clone(c.Members), id: core.ID(string(h[:]))}, nil
}
func (p *Provider) Identity() core.ID { return p.id }
func (p *Provider) Members(ctx context.Context, parent core.Block) (cap.Set, error) {
	if e := ctx.Err(); e != nil {
		return cap.Set{}, e
	}
	if parent == nil || parent.ID() == "" {
		return cap.Set{}, fmt.Errorf("static validators: parent required")
	}
	return cap.Set{Anchor: parent.ID(), ID: p.id, Members: clone(p.members)}, nil
}
