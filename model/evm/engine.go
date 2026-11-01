// Package evm connects go-ethereum's London transaction state transition and EVM
// to isolated GoChainKit snapshots. It does not implement an Ethereum network.
package evm

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
	adapter "github.com/lengzhao/GoChainKit/adapter/ethereum"
	"github.com/lengzhao/GoChainKit/core"
)

// Account is a genesis allocation or a query result. Storage allocations use
// exact 32-byte hexadecimal keys/values. Query storage separately with Storage.
type Account struct {
	Balance string            `json:"balance"`
	Nonce   uint64            `json:"nonce,omitempty"`
	Code    []byte            `json:"code,omitempty"`
	Storage map[string]string `json:"storage,omitempty"`
}

// Config fixes a London-from-genesis profile, a constant base fee, and a fixed
// fee recipient. All these policies and genesis allocations bind model identity.
// Post-London transactions and system operations are not enabled implicitly.
type Config struct {
	Network     string             `json:"network"`
	ChainID     string             `json:"chainID"`
	GasLimit    uint64             `json:"gasLimit"`
	BaseFee     string             `json:"baseFee"`
	Coinbase    string             `json:"coinbase"`
	Allocations map[string]Account `json:"allocations"`
}

type Engine struct {
	cfg      Config
	chain    *params.ChainConfig
	signer   types.Signer
	baseFee  *big.Int
	coinbase common.Address
	identity []byte
	codec    *adapter.Codec
}

func decimal(value string) (*big.Int, error) {
	n, ok := new(big.Int).SetString(value, 10)
	if !ok || n.Sign() < 0 || n.BitLen() > 256 {
		return nil, fmt.Errorf("evm: nonnegative uint256 decimal required")
	}
	return n, nil
}

func storageWord(value string) (common.Hash, error) {
	b, err := hex.DecodeString(strings.TrimPrefix(value, "0x"))
	if err != nil || len(b) != 32 {
		return common.Hash{}, fmt.Errorf("evm: exact 32-byte storage word required")
	}
	return common.BytesToHash(b), nil
}

func New(c Config) (*Engine, error) {
	if c.Network == "" || len(c.Network) > 128 {
		return nil, fmt.Errorf("evm: network required")
	}
	id, err := decimal(c.ChainID)
	if err != nil || id.Sign() == 0 {
		return nil, fmt.Errorf("evm: positive chain ID required")
	}
	if c.BaseFee == "" {
		c.BaseFee = "1"
	}
	fee, err := decimal(c.BaseFee)
	if err != nil {
		return nil, err
	}
	if c.GasLimit == 0 {
		c.GasLimit = 30000000
	}
	if c.GasLimit < 21000 || c.GasLimit > 100000000 {
		return nil, fmt.Errorf("evm: gas limit must be 21000..100000000")
	}
	if c.Coinbase == "" {
		c.Coinbase = common.Address{}.Hex()
	}
	if !common.IsHexAddress(c.Coinbase) {
		return nil, fmt.Errorf("evm: invalid coinbase address")
	}
	c.ChainID = id.String()
	c.BaseFee = fee.String()
	c.Coinbase = common.HexToAddress(c.Coinbase).Hex()
	allocations := make(map[string]Account, len(c.Allocations))
	total := 0
	for address, a := range c.Allocations {
		if !common.IsHexAddress(address) {
			return nil, fmt.Errorf("evm: invalid allocation address")
		}
		normalized := common.HexToAddress(address).Hex()
		if _, exists := allocations[normalized]; exists {
			return nil, fmt.Errorf("evm: duplicate allocation address")
		}
		if a.Balance == "" {
			a.Balance = "0"
		}
		balance, err := decimal(a.Balance)
		if err != nil {
			return nil, err
		}
		a.Balance = balance.String()
		if len(a.Code) > maxTransactionBytes {
			return nil, fmt.Errorf("evm: allocation code too large")
		}
		a.Code = bytes.Clone(a.Code)
		slots := make(map[string]string, len(a.Storage))
		for k, v := range a.Storage {
			key, err := storageWord(k)
			if err != nil {
				return nil, err
			}
			value, err := storageWord(v)
			if err != nil {
				return nil, err
			}
			if _, exists := slots[key.Hex()]; exists {
				return nil, fmt.Errorf("evm: duplicate storage key")
			}
			slots[key.Hex()] = value.Hex()
		}
		a.Storage = slots
		total += len(a.Code) + len(slots)*128 + 128
		if total > maxSnapshotBytes {
			return nil, fmt.Errorf("evm: genesis exceeds state budget")
		}
		allocations[normalized] = a
	}
	c.Allocations = allocations
	encoded, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}
	identity := sha256.Sum256(append([]byte("gochainkit/evm/london/1\x00"), encoded...))
	z := new(big.Int)
	chain := &params.ChainConfig{ChainID: id, HomesteadBlock: z, EIP150Block: z, EIP155Block: z, EIP158Block: z, ByzantiumBlock: z, ConstantinopleBlock: z, PetersburgBlock: z, IstanbulBlock: z, BerlinBlock: z, LondonBlock: z}
	codec, err := adapter.NewCodec(maxTransactionBytes)
	if err != nil {
		return nil, err
	}
	return &Engine{c, chain, types.NewLondonSigner(id), fee, common.HexToAddress(c.Coinbase), bytes.Clone(identity[:]), codec}, nil
}

func (e *Engine) Protocol() core.ProtocolDescriptor {
	return core.ProtocolDescriptor{Name: "evm-london", Version: "1/" + hex.EncodeToString(e.identity), Network: e.cfg.Network}
}
