// Package wallet signs UTXO transfers locally; the node never receives keys.
package wallet

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	utxoapi "github.com/lengzhao/GoChainKit/api/http/utxo"
	assetcap "github.com/lengzhao/GoChainKit/cap/asset"
	keycap "github.com/lengzhao/GoChainKit/cap/keystore"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/GoChainKit/internal/jsoncodec"
	"github.com/lengzhao/GoChainKit/model/asset"
	"github.com/lengzhao/GoChainKit/model/utxo"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Signed struct {
	ID   string `json:"id"`
	Data []byte `json:"data"`
}

// SignEncoded signs an explicitly constructed transaction offline. It performs
// stateless validation; input existence, ownership and maturity require a node.
func SignEncoded(key ed25519.PrivateKey, data []byte) (Signed, error) {
	if len(data) > 1<<20 {
		return Signed{}, fmt.Errorf("transaction too large")
	}
	var raw utxo.Transaction
	if err := jsoncodec.Strict(data, &raw); err != nil {
		return Signed{}, err
	}
	keys := make([]ed25519.PrivateKey, len(raw.Inputs))
	for i := range keys {
		keys[i] = key
	}
	if err := raw.Sign(keys...); err != nil {
		return Signed{}, err
	}
	tx, err := utxo.Wrap(raw)
	if err != nil {
		return Signed{}, err
	}
	encoded, err := json.Marshal(raw)
	return Signed{ID: hex.EncodeToString([]byte(tx.ID())), Data: encoded}, err
}

func Create(ctx context.Context, store keycap.Store, id keycap.ID) (assetcap.Address, error) {
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return assetcap.Address{}, err
	}
	defer clear(key)
	if err = store.Create(ctx, id, key); err != nil {
		return assetcap.Address{}, err
	}
	return asset.AddressOf(pub), nil
}
func Load(ctx context.Context, store keycap.Store, id keycap.ID) (ed25519.PrivateKey, error) {
	key, err := store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if len(key) != ed25519.PrivateKeySize || !bytes.Equal(ed25519.NewKeyFromSeed(key[:32]), key) {
		clear(key)
		return nil, fmt.Errorf("invalid wallet key")
	}
	return ed25519.PrivateKey(key), nil
}
func ParseAddress(raw string) (assetcap.Address, error) {
	var address assetcap.Address
	data, err := hex.DecodeString(raw)
	if err != nil || len(data) != 32 {
		return address, fmt.Errorf("address must be 64 hex characters")
	}
	copy(address[:], data)
	return address, nil
}

// BuildTransfer uses ordered, confirmed spendable coins; change returns to the
// signing key. Amount and fee are integer base units, with checked arithmetic.
func BuildTransfer(network string, key ed25519.PrivateKey, to assetcap.Address, amount, fee uint64, coins []utxoapi.Coin) (core.Transaction, error) {
	if len(key) != ed25519.PrivateKeySize || !bytes.Equal(ed25519.NewKeyFromSeed(key[:32]), key) || amount == 0 {
		return nil, fmt.Errorf("valid key and positive amount required")
	}
	required, err := asset.Add(amount, fee)
	if err != nil {
		return nil, err
	}
	raw := utxo.Transaction{Network: network, Outputs: []utxo.Output{{To: to, Amount: amount}}}
	var sum uint64
	var keys []ed25519.PrivateKey
	for _, coin := range coins {
		if !coin.Spendable {
			continue
		}
		id, err := hex.DecodeString(coin.TransactionID)
		if err != nil || len(id) != 32 || coin.Amount == 0 {
			return nil, fmt.Errorf("invalid coin response")
		}
		var point utxo.Outpoint
		copy(point.TransactionID[:], id)
		point.Index = coin.Index
		raw.Inputs = append(raw.Inputs, utxo.Input{Previous: point})
		keys = append(keys, key)
		if len(keys) > 128 {
			return nil, fmt.Errorf("too many inputs; consolidate coins")
		}
		sum, err = asset.Add(sum, coin.Amount)
		if err != nil {
			return nil, err
		}
		if sum >= required {
			break
		}
	}
	if sum < required {
		return nil, fmt.Errorf("insufficient spendable funds: have %d, need %d", sum, required)
	}
	if sum > required {
		raw.Outputs = append(raw.Outputs, utxo.Output{To: asset.AddressOf(key[32:]), Amount: sum - required})
	}
	if err = raw.Sign(keys...); err != nil {
		return nil, err
	}
	return utxo.Wrap(raw)
}

type Client struct {
	base string
	http *http.Client
}

func NewClient(base string) (*Client, error) {
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("valid HTTP API URL required")
	}
	return &Client{strings.TrimRight(base, "/"), &http.Client{Timeout: 15 * time.Second}}, nil
}
func (c *Client) Request(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		data, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, (8<<20)+1))
	if err != nil {
		return err
	}
	if len(data) > 8<<20 {
		return fmt.Errorf("API response too large")
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("API %d: %.256s", res.StatusCode, data)
	}
	return json.Unmarshal(data, out)
}
func (c *Client) Balance(ctx context.Context, address assetcap.Address) (utxoapi.Balance, error) {
	owner := hex.EncodeToString(address[:])
	out := utxoapi.Balance{}
	offset := 0
	for pages := 0; pages < 4096; pages++ {
		path := "/v1/addresses/" + owner + "/utxos?limit=256&offset=" + strconv.Itoa(offset)
		if out.At != "" {
			path += "&at=" + url.QueryEscape(out.At)
		}
		var page utxoapi.Balance
		if err := c.Request(ctx, "GET", path, nil, &page); err != nil {
			return out, err
		}
		if page.Address != owner || page.At == "" || (out.At != "" && (out.At != page.At || out.Height != page.Height || out.Spendable != page.Spendable || out.Immature != page.Immature)) {
			return out, fmt.Errorf("inconsistent balance response")
		}
		if offset == 0 {
			out = page
		} else {
			out.Coins = append(out.Coins, page.Coins...)
		}
		if page.NextOffset == 0 {
			out.NextOffset = 0
			return out, nil
		}
		if page.NextOffset != offset+len(page.Coins) || page.NextOffset <= offset {
			return out, fmt.Errorf("invalid pagination cursor")
		}
		offset = page.NextOffset
	}
	return out, fmt.Errorf("wallet output listing limit exceeded")
}

type Admission struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	BlockID string `json:"blockId"`
}

func (c *Client) Send(ctx context.Context, key ed25519.PrivateKey, to assetcap.Address, amount, fee uint64) (Admission, error) {
	if len(key) != ed25519.PrivateKeySize {
		return Admission{}, fmt.Errorf("invalid wallet key")
	}
	var node struct {
		Protocol struct {
			Network string `json:"network"`
		} `json:"protocol"`
	}
	if err := c.Request(ctx, "GET", "/v1/node", nil, &node); err != nil {
		return Admission{}, err
	}
	balance, err := c.Balance(ctx, asset.AddressOf(key[32:]))
	if err != nil {
		return Admission{}, err
	}
	tx, err := BuildTransfer(node.Protocol.Network, key, to, amount, fee, balance.Coins)
	if err != nil {
		return Admission{}, err
	}
	raw, err := utxo.Unwrap(tx)
	if err != nil {
		return Admission{}, err
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return Admission{}, err
	}
	var out Admission
	err = c.Request(ctx, "POST", "/v1/transactions", map[string]string{"data": base64.StdEncoding.EncodeToString(data)}, &out)
	if err == nil && out.ID != hex.EncodeToString([]byte(tx.ID())) {
		err = fmt.Errorf("admission transaction ID mismatch")
	}
	return out, err
}
