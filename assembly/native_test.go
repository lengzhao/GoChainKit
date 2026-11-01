package assembly

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/txscript/v2"
	"github.com/btcsuite/btcd/wire/v2"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	httpcap "github.com/lengzhao/GoChainKit/cap/http"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/GoChainKit/internal/modeltest"
	btc "github.com/lengzhao/GoChainKit/model/bitcoin"
	_ "github.com/lengzhao/GoChainKit/plugins/all"
	"github.com/lengzhao/pluginkit/build"
)

func TestNativePresetHTTPSubmitAndReopen(t *testing.T) {
	for _, kind := range []string{"bitcoin", "evm"} {
		t.Run(kind, func(t *testing.T) {
			data, err := os.ReadFile("../presets/native-" + kind + ".yaml")
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			lines := strings.Split(string(data), "\n")
			for i, line := range lines {
				if strings.HasPrefix(line, "  keys:") {
					lines[i] = "  keys: {use: keystore/memory}"
				}
			}
			cfg := strings.Join(lines, "\n")
			cfg = strings.ReplaceAll(cfg, "data/", filepath.ToSlash(dir)+"/")
			for _, port := range []string{"8091", "8092"} {
				cfg = strings.ReplaceAll(cfg, "127.0.0.1:"+port, "127.0.0.1:0")
			}
			for _, port := range []string{"19401", "19501"} {
				cfg = strings.ReplaceAll(cfg, "tcp/"+port, "tcp/0")
			}
			cfg = strings.ReplaceAll(cfg, "interval: 1s", "interval: 50ms")
			var previous string
			var input wire.OutPoint
			var script []byte
			client := &http.Client{Timeout: 3 * time.Second}
			for phase := 0; phase < 2; phase++ {
				root, result, err := Load(context.Background(), strings.NewReader(cfg))
				if err != nil {
					t.Fatal(err)
				}
				apiServer, err := build.RequireByID[httpcap.Server](result, "api", "api")
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithCancel(context.Background())
				done := make(chan error, 1)
				go func() { done <- root.Run(ctx) }()
				stopped := false
				stop := func() {
					if !stopped {
						stopped = true
						cancel()
						select {
						case err := <-done:
							if err != nil {
								t.Error(err)
							}
						case <-time.After(8 * time.Second):
							t.Error("native preset shutdown timed out")
						}
					}
				}
				t.Cleanup(stop)
				deadline := time.Now().Add(10 * time.Second)
				for len(apiServer.Addresses()) == 0 {
					select {
					case err := <-done:
						stopped = true
						cancel()
						t.Fatalf("native preset stopped: %v", err)
					default:
					}
					if time.Now().After(deadline) {
						t.Fatal("native API did not start")
					}
					time.Sleep(time.Millisecond)
				}
				base := apiServer.Addresses()[0]
				if !strings.HasPrefix(base, "http://") {
					base = "http://" + base
				}
				get := func(path string) (int, []byte) {
					t.Helper()
					response, err := client.Get(base + path)
					if err != nil {
						t.Fatal(err)
					}
					defer response.Body.Close()
					raw, err := io.ReadAll(response.Body)
					if err != nil {
						t.Fatal(err)
					}
					return response.StatusCode, raw
				}
				if phase > 0 {
					code, raw := get("/v1/transactions/" + previous)
					if code != 200 || !bytes.Contains(raw, []byte(`"confirmed"`)) {
						t.Fatalf("previous transaction not recovered: %d %s", code, raw)
					}
				}
				var raw []byte
				if kind == "bitcoin" {
					e, err := build.RequireByID[*btc.Engine](result, "model", "model")
					if err != nil {
						t.Fatal(err)
					}
					if phase == 0 {
						input = e.GenesisOutpoints()[0]
						state, err := e.Genesis(ctx)
						if err != nil {
							t.Fatal(err)
						}
						out, err := e.Output(ctx, modeltest.Reader(state), input)
						if err != nil {
							t.Fatal(err)
						}
						script = out.Script
					}
					key, _ := btcec.PrivKeyFromBytes(bytes.Repeat([]byte{1}, 32))
					tx := wire.NewMsgTx(2)
					tx.AddTxIn(wire.NewTxIn(&input, nil, nil))
					tx.AddTxOut(wire.NewTxOut(int64(90000000-phase*10000000), script))
					sig, err := txscript.RawTxInSignature(tx, 0, script, txscript.SigHashAll, key)
					if err != nil {
						t.Fatal(err)
					}
					tx.TxIn[0].SignatureScript, err = txscript.NewScriptBuilder().AddData(sig).Script()
					if err != nil {
						t.Fatal(err)
					}
					var buf bytes.Buffer
					if err = tx.Serialize(&buf); err != nil {
						t.Fatal(err)
					}
					raw = buf.Bytes()
					input = wire.OutPoint{Hash: tx.TxHash(), Index: 0}
				} else {
					key, err := crypto.ToECDSA(bytes.Repeat([]byte{1}, 32))
					if err != nil {
						t.Fatal(err)
					}
					to := common.Address{2}
					tx, err := types.SignTx(types.NewTx(&types.LegacyTx{Nonce: uint64(phase), To: &to, Value: big.NewInt(7), Gas: 21000, GasPrice: big.NewInt(2)}), types.LatestSignerForChainID(big.NewInt(1337)), key)
					if err != nil {
						t.Fatal(err)
					}
					raw, err = tx.MarshalBinary()
					if err != nil {
						t.Fatal(err)
					}
				}
				body, _ := json.Marshal(struct {
					Data []byte `json:"data"`
				}{raw})
				response, err := client.Post(base+"/v1/transactions", "application/json", bytes.NewReader(body))
				if err != nil {
					t.Fatal(err)
				}
				payload, err := io.ReadAll(response.Body)
				response.Body.Close()
				if err != nil {
					t.Fatal(err)
				}
				if response.StatusCode != 202 {
					t.Fatalf("submit %d: %s", response.StatusCode, payload)
				}
				var accepted struct {
					ID string `json:"id"`
				}
				if err = json.Unmarshal(payload, &accepted); err != nil || accepted.ID == "" {
					t.Fatalf("submit result: %s %v", payload, err)
				}
				previous = accepted.ID
				confirmed := false
				deadline = time.Now().Add(10 * time.Second)
				for time.Now().Before(deadline) {
					code, payload := get("/v1/transactions/" + previous)
					if code == 200 && bytes.Contains(payload, []byte(`"confirmed"`)) {
						confirmed = true
						break
					}
					time.Sleep(10 * time.Millisecond)
				}
				if !confirmed {
					t.Fatal("native transaction never confirmed")
				}
				if code, payload := get("/v1/receipts/" + previous); code != 200 {
					t.Fatalf("receipt %d %s", code, payload)
				}
				if kind == "evm" {
					code, payload := get("/v1/evm/accounts/" + common.Address{2}.Hex())
					if code != 200 || !bytes.Contains(payload, []byte(fmt.Sprintf(`"balance":"%d"`, 7*(phase+1)))) {
						t.Fatalf("EVM state %d %s", code, payload)
					}
				}
				ledger, err := build.RequireByID[core.LedgerReader](result, "ledger", "ledger")
				if err != nil {
					t.Fatal(err)
				}
				if _, err = ledger.Head(ctx); err != nil {
					t.Fatal(err)
				}
				stop()
			}
		})
	}
}
