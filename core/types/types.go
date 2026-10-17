// Package types defines the draft GoChainKit protocol. Encoding changes require
// protocol review; this initial version is not a production network protocol.
package types

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"math"
	"sort"
)

const (
	MaxChainIDBytes = 128
	MaxKeyBytes     = 1024
	MaxValueBytes   = 1 << 20
)

type Hash [sha256.Size]byte
type Address [sha256.Size]byte

// Transaction writes a key in the sender's namespace. Nonce starts at one.
// Callers own its slices; consumers retaining a transaction must clone it.
type Transaction struct {
	ChainID   string
	Nonce     uint64
	PublicKey []byte
	Key       []byte
	Value     []byte
	Signature []byte
}

func field(b *bytes.Buffer, v []byte) {
	_ = binary.Write(b, binary.BigEndian, uint32(len(v)))
	_, _ = b.Write(v)
}

func (t Transaction) payload() ([]byte, error) {
	if len(t.ChainID) == 0 || len(t.ChainID) > MaxChainIDBytes || t.Nonce == 0 || t.Nonce == math.MaxUint64 || len(t.Key) == 0 || len(t.Key) > MaxKeyBytes || len(t.Value) > MaxValueBytes || len(t.PublicKey) != ed25519.PublicKeySize {
		return nil, errors.New("invalid transaction fields")
	}
	var b bytes.Buffer
	b.WriteString("gochainkit/tx-sign/v1\x00")
	field(&b, []byte(t.ChainID))
	_ = binary.Write(&b, binary.BigEndian, t.Nonce)
	field(&b, t.PublicKey)
	field(&b, t.Key)
	field(&b, t.Value)
	return b.Bytes(), nil
}

// Sign sets the public key and signature. A private key must be canonical.
func (t *Transaction) Sign(key ed25519.PrivateKey) error {
	if t == nil || len(key) != ed25519.PrivateKeySize {
		return errors.New("invalid private key or nil transaction")
	}
	canonical := ed25519.NewKeyFromSeed(key[:ed25519.SeedSize])
	if !bytes.Equal(canonical, key) {
		return errors.New("noncanonical private key")
	}
	t.PublicKey = bytes.Clone(key[ed25519.SeedSize:])
	payload, err := t.payload()
	if err != nil {
		return err
	}
	t.Signature = ed25519.Sign(key, payload)
	return nil
}

// Verify rejects malformed fields, cross-chain transactions and invalid signatures.
func (t Transaction) Verify(chainID string) error {
	if t.ChainID != chainID {
		return errors.New("wrong chain ID")
	}
	payload, err := t.payload()
	if err != nil {
		return err
	}
	if len(t.Signature) != ed25519.SignatureSize || !ed25519.Verify(t.PublicKey, payload, t.Signature) {
		return errors.New("invalid signature")
	}
	return nil
}

// MarshalBinary returns deterministic length-prefixed bytes; it is not a decoder.
func (t Transaction) MarshalBinary() ([]byte, error) {
	p, err := t.payload()
	if err != nil {
		return nil, err
	}
	if len(t.Signature) != ed25519.SignatureSize {
		return nil, errors.New("invalid signature size")
	}
	var b bytes.Buffer
	b.WriteString("gochainkit/tx/v1\x00")
	field(&b, p)
	field(&b, t.Signature)
	return b.Bytes(), nil
}

func (t Transaction) ID() (Hash, error) {
	b, err := t.MarshalBinary()
	if err != nil {
		return Hash{}, err
	}
	return sha256.Sum256(b), nil
}

func (t Transaction) Sender() Address { return sha256.Sum256(t.PublicKey) }

func (t Transaction) Clone() Transaction {
	t.PublicKey = bytes.Clone(t.PublicKey)
	t.Key = bytes.Clone(t.Key)
	t.Value = bytes.Clone(t.Value)
	t.Signature = bytes.Clone(t.Signature)
	return t
}

// StateRoot hashes sorted, length-prefixed full state keys. Account nonces must
// be included in this map by the executor. This is not a Merkle proof API.
func StateRoot(state map[string][]byte) Hash {
	keys := make([]string, 0, len(state))
	for k := range state {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b bytes.Buffer
	b.WriteString("gochainkit/state/v1\x00")
	for _, k := range keys {
		field(&b, []byte(k))
		field(&b, state[k])
	}
	return sha256.Sum256(b.Bytes())
}

type BlockHeader struct {
	Version         uint32
	ChainID         string
	Height          uint64
	Parent          Hash
	TransactionRoot Hash
	StateRoot       Hash
	ReceiptRoot     Hash
}
type Block struct {
	Header       BlockHeader
	Transactions []Transaction
}
type Receipt struct {
	TransactionID Hash
	Success       bool
	ErrorCode     string
}

// ExecutionResult is an isolated proposal. Committers must validate its binding
// to the parent and block before accepting it, and copy retained slices/maps.
type ExecutionResult struct {
	Parent    Hash
	BlockID   Hash
	Writes    map[string][]byte
	Receipts  []Receipt
	StateRoot Hash
}
type Genesis struct {
	ChainID      string
	Version      uint32
	InitialState map[string][]byte
}
