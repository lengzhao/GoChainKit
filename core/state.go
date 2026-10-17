package core

import (
	"bytes"
	"context"
	"fmt"
)

// ApplyStateChanges is the common state-transition rule used by models and
// storage. Nil/empty values are writes; deletions are explicit and disjoint.
// The returned state owns all bytes and the parent is never mutated.
func ApplyStateChanges(ctx context.Context, parent, writes map[string][]byte, deletes []string) (map[string][]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	removed := make(map[string]bool, len(deletes))
	for _, key := range deletes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if removed[key] {
			return nil, fmt.Errorf("duplicate state deletion")
		}
		if _, exists := writes[key]; exists {
			return nil, fmt.Errorf("state key both written and deleted")
		}
		removed[key] = true
	}
	next := make(map[string][]byte, len(parent)+len(writes))
	for key, value := range parent {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !removed[key] {
			next[key] = bytes.Clone(value)
		}
	}
	for key, value := range writes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		next[key] = bytes.Clone(value)
	}
	return next, nil
}
