# 第一笔签名交易：提交、确认、重开

本教程使用 `protocol/kv` 的命名空间键值交易及 `presets/single.yaml`。它不是账户支付：没有余额、手续费或测试币前置条件，因此不需要造币/水龙头。账户/UTXO 的创世资金必须由模型配置显式提供，不能把本例当作资金注入方式。

要求：在仓库根目录运行，Go 1.26.1、curl、Python 3，8080 空闲。所有账本/示例文件写入新临时目录。示例随机生成一次性 Ed25519 身份，只保留在进程内，不打印/保存私钥；它不能作为生产钱包。已签请求保存后可原样重试，但第二笔交易需保留原身份并按链上状态递增 nonce，不能照抄 nonce=1。

## 1. 准备运行目录与二进制

```sh
set -e
GCK_REPO="$PWD"
GCK_DEMO="$(mktemp -d "${TMPDIR:-/tmp}/gochainkit-tx.XXXXXX")"
go build -o "$GCK_DEMO/node" ./cmd/gochainkit
```

## 2. 创建客户端

复制下面程序到 `$GCK_DEMO/submit.go`。它使用公开包，先 `Sign`、`WrapTransaction`，再调用所选 Protocol 的 `EncodeTransaction`。不要把 `MarshalBinary` 当作 HTTP 协议编码：KV 的 ID/签名字节与 `EncodeTransaction` 返回的线上 JSON 字节职责不同。

```go
package main

import (
    "bytes"
    "crypto/ed25519"
    "crypto/rand"
    "encoding/hex"
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "os"
    "path/filepath"
    "time"

    "github.com/lengzhao/GoChainKit/protocol/kv"
)

func must(err error) { if err != nil { panic(err) } }
func main() {
    if len(os.Args) != 2 { panic("usage: submit <temporary directory>") }
    _, key, err := ed25519.GenerateKey(rand.Reader); must(err)
    tx := kv.Transaction{ChainID: "dev", Nonce: 1, Key: []byte("hello"), Value: []byte("world")}
    must(tx.Sign(key))
    wrapped, err := kv.WrapTransaction(tx); must(err)
    protocol, err := kv.New(kv.Config{ChainID: "dev"}); must(err)
    encoded, err := protocol.EncodeTransaction(wrapped); must(err)
    request, err := json.Marshal(struct { Data []byte `json:"data"` }{encoded}); must(err)
    id := hex.EncodeToString([]byte(wrapped.ID()))
    must(os.WriteFile(filepath.Join(os.Args[1], "request.json"), request, 0600))
    must(os.WriteFile(filepath.Join(os.Args[1], "id.txt"), []byte(id), 0600))
    endpoint := os.Getenv("GCK_API")
    if endpoint == "" { endpoint = "http://127.0.0.1:8080" }
    client := &http.Client{Timeout: 2 * time.Second}
    res, err := client.Post(endpoint + "/v1/transactions", "application/json", bytes.NewReader(request)); must(err)
    body, err := io.ReadAll(res.Body); res.Body.Close(); must(err)
    if res.StatusCode != 202 && res.StatusCode != 200 { panic(fmt.Sprintf("submit %d: %s", res.StatusCode, body)) }
    fmt.Printf("submit %s: %s\n", id, body)
    deadline := time.Now().Add(8 * time.Second)
    for time.Now().Before(deadline) {
        res, err = client.Get(endpoint + "/v1/transactions/" + id); must(err)
        body, err = io.ReadAll(res.Body); res.Body.Close(); must(err)
        var result struct { Status string `json:"status"` }
        if res.StatusCode == 200 { must(json.Unmarshal(body, &result)) }
        if result.Status == "confirmed" { fmt.Printf("confirmed: %s\n", body); return }
        time.Sleep(200 * time.Millisecond)
    }
    panic("not confirmed within 8s; inspect node log and query by saved ID before retrying")
}
```

## 3. 启动并提交

```sh
(cd "$GCK_DEMO" && ./node --config "$GCK_REPO/presets/single.yaml" --run-for 15s) >"$GCK_DEMO/node.log" 2>&1 &
GCK_NODE_PID=$!
# 最多等 5 秒；探活失败时查看日志，不直接向未知端口提交。
GCK_READY=0
for GCK_TRY in 1 2 3 4 5; do
  if curl -fsS http://127.0.0.1:8080/v1/node >"$GCK_DEMO/node.json"; then GCK_READY=1; break; fi
  sleep 1
done
test "$GCK_READY" = 1
# 确认 node.json 的 protocol 是 kv / 1 / dev，且日志没有端口占用或启动失败。
cat "$GCK_DEMO/node.json"
go run "$GCK_DEMO/submit.go" "$GCK_DEMO"
wait "$GCK_NODE_PID"
```

如果 `test`、客户端或 `wait` 失败，停止后续步骤并查看 `$GCK_DEMO/node.log`。不要把失败输出当作确认。预期 submit 为 202（或已确认时 200），随后打印 `confirmed`。HTTP accepted 只表示入池；confirmed 也只代表该次 canonical 观察，不是 BFT 最终性。

## 4. 重开并按同一 ID 查询

```sh
(cd "$GCK_DEMO" && ./node --config "$GCK_REPO/presets/single.yaml" --run-for 8s) >"$GCK_DEMO/reopen.log" 2>&1 &
GCK_NODE_PID=$!
GCK_READY=0
for GCK_TRY in 1 2 3 4 5; do
  if curl -fsS http://127.0.0.1:8080/v1/node >"$GCK_DEMO/reopen-node.json"; then GCK_READY=1; break; fi
  sleep 1
done
test "$GCK_READY" = 1
GCK_TX_ID="$(cat "$GCK_DEMO/id.txt")"
curl -fsS "http://127.0.0.1:8080/v1/transactions/$GCK_TX_ID" >"$GCK_DEMO/reopened-tx.json"
python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); assert d["status"] == "confirmed"; print(d["id"], d["status"])' "$GCK_DEMO/reopened-tx.json"
curl -fsS "http://127.0.0.1:8080/v1/receipts/$GCK_TX_ID"
wait "$GCK_NODE_PID"
```

保留临时目录供检查；这里不提供自动删除命令。重开必须使用同一数据库与协议配置；本地等待超时不说明交易未受理，先按保存 ID 查询再决定重发。端口占用时复制 single preset 到临时目录并修改 api.config.listen，启动改用该配置；设置命令级 GCK_API 指向相同 URL（客户端读取它），并同步修改 curl URL。不要连接或停止占用原端口的未知服务。

## 验证口径

本教程以当前公开 KV API 和 single preset 编写。实测记录应包含 Go 版本、提交状态、确认、节点退出和重开查询；正常关闭重开验证不等于掉电恢复测试。更广的故障与组合覆盖见 [扩展验证](../contributing/testing.md)。

2026-10-22：从本页提取的 Go 客户端已在 Go 1.26.1 与 single 配置副本完成实测，使用独立端口及 GCK_API，确认提交、confirmed、正常退出、同库重开后的交易/回执与区块绑定。默认 8080 命令未直接执行（已有服务占用），未执行硬件掉电测试。
