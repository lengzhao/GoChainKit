# BTC/ETH 原生组件服务

本层复用成熟实现，不引入新的交易模型。`adapter/bitcoin` 使用 btcd 的 `wire/v2`、`txscript/v2`；`adapter/ethereum` 使用 go-ethereum 的 `core/types`。调用方直接获得上游原生交易类型，可继续调用上游签名、地址、脚本构造等能力。适配器只负责有界严格编解码和明确的验证入口。

当前固定 wire/v2 与 txscript/v2 为 v2.0.0，go-ethereum 为 v1.17.10；更新依赖时需要重新验证上游语义。BTC 更新版模块要求 Go 1.26.10，本次保持仓库 Go 1.26.1 工具链。Geth 的 core/types 会引入原生交易所需的密码学依赖，但服务不启动 Geth 客户端。

## 直接复用

```go
btc, err := bitcoin.New(bitcoin.Config{})
// 检查 err；raw 是 Bitcoin 原生交易字节。
tx, err := btc.Decode(raw) // *wire.MsgTx
err = btc.VerifyScripts(ctx, tx, prevouts) // map[wire.OutPoint]*wire.TxOut

eth, err := ethereum.New(ethereum.Config{ChainID: "1337"})
txETH, err := eth.Decode(rawETH) // *types.Transaction
sender, err := eth.Sender(ctx, txETH) // common.Address
```

导入路径分别为 `github.com/lengzhao/GoChainKit/adapter/bitcoin` 和 `github.com/lengzhao/GoChainKit/adapter/ethereum`。上例省略常规错误处理，实际应用应逐次检查。适配器没有 I/O 和关闭资源，可并发调用；调用期间不得并发修改交易、prevout 或输入字节。Decode/Encode 返回调用方拥有的数据。

BTC 默认交易限额 4 MiB，脚本使用上游 `StandardVerifyFlags`。可通过 `Config.ScriptFlags` 显式指定上游标志（指针值 0 表示不启用标志）；这是脚本策略选择，不是完整 Bitcoin 共识验证。ETH 默认限额 1 MiB，要求显式正的十进制 uint256 chain ID。两者可配置的本地交易限额上限为 4 MiB。

## 配置装配

```sh
go run ./cmd/gochainkit -config presets/transactions.yaml
```

默认监听 `127.0.0.1:8090`，不要求数据库、钱包口令、P2P 或共识。配置中的 `transactions/bitcoin`、`transactions/ethereum` 是独立实例，`service/transactions` 通过类型化 Deps 消费它们。删除对应插件实例及 service 的对应依赖即可只运行 BTC 或 ETH。也可自写 Go 服务直接注入适配器、复用 `Server.Handler()`，无需运行当前 CLI。

根服务实现 `core.Node`，构造不监听；Run 启动监听、监督 HTTP server，并在取消时撤销请求和关闭监听。每个实例只运行一次，重启需重新装配。默认请求体限制 12 MiB、并发上限 8，超额请求返回 413/429；请求上下文限时 10 秒，读写限时 15 秒，关闭宽限 5 秒。原生单次密码学操作无法在中途取消，在各操作之间检查 context。默认不提供鉴权，按本机工具服务使用。

| 方法与路径 | 能力 |
| --- | --- |
| `GET /healthz` | 工具服务存活，不表示链同步或网络就绪 |
| `POST /v1/bitcoin/decode` | 严格解析并返回原生编码及 txid |
| `POST /v1/bitcoin/verify-scripts` | 使用调用方提供的 prevout 验证所有输入脚本 |
| `POST /v1/ethereum/decode` | 严格解析并返回原生编码及交易哈希 |
| `POST /v1/ethereum/sender` | 验证签名并按配置 chain ID 恢复发送者 |

未装配的链不会注册对应路由。所有 POST 接收单个严格 JSON 对象，拒绝未知字段和尾随 JSON；`data` 是 Base64 编码的原生交易字节，输出为 `{id,data}`，ETH sender 额外返回 `sender`。BTC ID 采用原生 TxHash 的展示形式，ETH 使用原生 Hash.Hex；不转换成 GoChainKit 自定义交易 ID。

```json
{"data":"<Base64 原生交易>"}
```

BTC verify-scripts 另需 `prevouts`，覆盖每个输入（Taproot sighash 预计算也需要全部输入）。交易引用为 64 位十六进制展示哈希，金额以最小单位 satoshi 的十进制字符串表示，script 是 Base64。重复 prevout 被拒绝。

```json
{
  "data": "<Base64 原生交易>",
  "prevouts": [{
    "transactionID": "0000000000000000000000000000000000000000000000000000000000000000",
    "index": 1,
    "value": "1000",
    "script": "UQ=="
  }]
}
```

解析失败返回 400，脚本或发送者验证失败返回 422，取消/超时为 408/504。错误体形如 `{"error":"verification_failed"}`，不回显上游输入和错误详情。

## 验证边界

BTC 服务仅证明交易在**调用方提供的 prevout** 下通过脚本策略；不证明输出真实存在、未花费，不验证全套金额、费用、成熟期、timelock 或区块上下文规则。`decode` 只验证编码，不证明交易有效。BTC witness 保留在交易编码里，txid 仍按上游定义不包含 witness。

ETH sender 拒绝未签名、无重放保护和错误 chain ID 的交易。签名恢复使用上游 LatestSignerForChainID，不按区块高度判断交易类型是否已激活，也不验证余额、nonce、费用、intrinsic gas 或执行结果。支持某类型的编码与恢复不意味着已验证 blob sidecar 等附加数据。

本页描述独立无状态工具服务。需要有状态执行、EVM、出块、持久化和节点同步时，使用后续提供的 [原生模型完整自定义链](native-chains.md) 装配；两种入口可以独立使用。没有接入 keyring、HTTP 私钥服务或完整 BTC/ETH 公链共识。

上游参考：[btcd txscript](https://github.com/btcsuite/btcd/tree/master/txscript)、[go-ethereum core/types](https://github.com/ethereum/go-ethereum/tree/master/core/types)。

## 按需编译与公共实现

自定义二进制可分别导入 `plugins/model/bitcoin`、`plugins/model/evm`、`plugins/transactions/bitcoin`、`plugins/transactions/ethereum`、`plugins/api/bitcoin`、`plugins/api/evm`；每个包只注册自身实现。BTC 的窄模型、工具适配器和状态 API 不引入 Ethereum/EVM 包。`plugins/all` 仍用于包含所有内置能力的二进制。

旧的 `plugins/model`、`plugins/transactions`、`plugins/api/native` 保留兼容聚合入口；旧 `api/http/native` 保留薄别名和构造包装。插件 Kind、配置字段和 HTTP 路由不变。组合工具服务本身同时支持 BTC/ETH，其注册入口为 `plugins/service/transactions`。

链 HTTP 与组合工具服务共用 `runtime/http` 的监听、请求预算、并发、超时和关闭实现。公共运行器只依赖 Handler 和错误渲染策略，账本/协议一致性检查仍由链 API 装配负责。运行器地址统一为 HTTP URL；工具服务的兼容 facade 保留原 `host:port` 地址表示。

Ethereum 工具和 EVM 模型共享 `adapter/ethereum.Codec` 的 canonical 编解码；工具保留 LatestSigner，EVM 保留 London 准入策略。模型冻结并缓存解析结果，但执行时仍校验当前实例策略及真实链状态。EVM 的私有 `stateSession` 负责独立 rawdb/trie 的恢复、预算、提交、差量和关闭，不改变账本持久格式。
