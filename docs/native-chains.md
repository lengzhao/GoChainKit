# 原生 BTC/EVM 模型的完整自定义链

`model/bitcoin` 和 `model/evm` 将原生交易接入现有 `composed` 协议。节点拥有交易池、执行、出块、回执、分支账本、Bolt 持久化、P2P 同步和 HTTP 查询。适配层保留原生交易编码和签名规则，区块封装、共识和网络仍属于所装配的自定义链。

## 启动

先设置本地 P2P 身份加密口令环境变量 `GOCHAINKIT_KEYSTORE_PASSWORD`；重启使用相同口令。该口令不用于原生交易签名，节点 HTTP 不接收私钥。

```sh
go run ./cmd/gochainkit -config presets/native-bitcoin.yaml
go run ./cmd/gochainkit -config presets/native-evm.yaml
```

两个命令可独立运行。BTC API 为 `http://127.0.0.1:8091`、P2P 为 19401；EVM API 为 `http://127.0.0.1:8092`、P2P 为 19501。各自使用独立的数据库和身份目录。`-run-for 5s` 可作有界启动检查。

预设是开发链：均给**私钥字节 `01` 重复 32 次**对应的公钥预分配资产，只用于本地验证。BTC 预分配 100000000 satoshi 到该公钥的 P2PK 脚本；EVM 预分配 100 ETH 单位（100000000000000000000 wei）到 `0x1a642f0e3c3af545e7acbd38b07251b3990914f1`。正式使用需替换创世分配、chain ID 和本地节点参数；改变创世或执行语义应使用新数据目录。

## 交易提交及确认

通过 btcd/txscript 或 go-ethereum/core/types 在客户端构造并签名，使用原生 wire/envelope 编码，将字节 Base64 后提交：

```sh
curl http://127.0.0.1:8091/v1/bitcoin/genesis
curl http://127.0.0.1:8091/v1/chain/head
curl -X POST http://127.0.0.1:8091/v1/transactions \
  -H 'Content-Type: application/json' \
  -d '{"data":"<Base64 原生签名交易>"}'
```

EVM 同样通过其 8092 端口提交原生签名交易。202 表示本地受理，随后通过返回的 `id` 查询 `/v1/transactions/{id}` 和 `/v1/receipts/{id}`。有状态规则在出块时重验，受理不保证确认。BTC 未知来源输入、未到期锁定和 EVM 未来 nonce/余额不足可暂缓；已知双花、错误签名或过期 nonce 被拒绝。

通用 API 的 `id` 是框架不透明 ID 原始字节的十六进制。BTC 原生 txid 展示字符串采用反向字节序，因此**不要用 BTC 展示 txid 替代提交响应中的通用查询 id**。BTC 专属 UTXO 路由则接受 btcd 的 `TxHash().String()`。

可运行的真实原生签名构造、HTTP 提交和重启续转示例见 `assembly/native_test.go`；模型测试还提供脚本签名和合约字节码示例。无需修改上游交易来适配 GoChainKit 钱包。

## BTC 执行及查询

BTC 复用 btcd `CheckTransactionSanity`、`CheckTransactionInputs`、`IsFinalizedTransaction`、`SequenceLockActive` 和 txscript 引擎。模型状态保存 UTXO 的值、原生脚本、创建高度和创建前的父链中位时间，用于 BIP68 相对锁定。11 块中位时间沿执行父分支计算，普通交易不能提供虚假的 prevout。

- 原生编码严格往返，保留 witness；消费授权使用上游标准脚本策略。
- 校验金额范围、输入可用性、守恒、批内依赖、双花、绝对锁定及相对高度/时间锁定。
- 创世输出引用由完整模型配置确定；已执行交易 marker 防止重复执行及覆盖历史输出。
- 当前供给来自创世，不接受用户 coinbase，不增发区块奖励，手续费销毁。未实现 Bitcoin 全套历史软分叉激活及 coinbase 发行规则。

| 路由 | 返回 |
| --- | --- |
| `GET /v1/bitcoin/genesis` | 开发链创世分配的原生 outpoint 引用 |
| `GET /v1/bitcoin/utxos/{txid}/{index}` | 当前未花费输出及元数据 |

UTXO 查询支持 `?at=<框架区块 ID>`；历史快照可以读取后来已花费的输出，当前已消费输出返回 404。金额为 satoshi 十进制字符串，script 是 Base64。默认单交易上限 1 MiB，支持高度上限 int32。

## EVM 执行及查询

EVM 完整复用 geth StateDB、MPT、交易状态转换和虚拟机，执行真实合约字节码，包括部署、存储、日志、Gas、手续费、nonce 和 REVERT。REVERT 是已执行失败交易：回滚合约变更，同时保留 Gas 收取、nonce 推进和失败回执。

当前执行 profile 固定为 **London**，仅接受 replay-protected Legacy、EIP-2930 和 EIP-1559 交易。chain ID、固定 base fee、固定手续费受益人 coinbase、Gas 上限和创世分配均绑定模型身份。默认 base fee 为 1 wei；base fee 销毁，tip 支付给配置的 coinbase（默认零地址）。这不是 Ethereum PoS 或动态 EIP-1559 base fee 的实现。

每次执行从父分支快照恢复独立的上游内存 rawdb/trie，完成后将原生 KV/root 和上下文元数据转换成框架写删集合。账本把状态、区块、回执和链头原子保存；无效候选块没有外部状态副作用。不同分支不共享可变 StateDB。

| 路由 | 返回 |
| --- | --- |
| `GET /v1/evm/accounts/{address}` | 余额、nonce、code |
| `GET /v1/evm/storage/{address}/{key}` | 一个 32 字节 storage slot |
| `POST /v1/evm/call` | 只读 EVM 执行，返回 data、gasUsed、reverted |

这些路由均支持 `?at=<框架区块 ID>`。地址是原生 20 字节十六进制，slot 为完整 32 字节十六进制，code/data 为 Base64。金额、nonce 和 Gas 在 HTTP JSON 中使用十进制字符串。Call 参数示例：

```json
{"from":"0x0000000000000000000000000000000000000000","to":"<合约地址>","data":"<Base64 calldata>","value":"0","gas":"100000"}
```

调用使用锚定块的 NUMBER/TIMESTAMP 和前 256 个祖先哈希，不使用正在推进的链头。它运行在隔离状态上，不提交写入。REVERT/OutOfGas 返回 422，REVERT 仍返回 returndata；参数错误为 400，状态恢复或历史故障保留为服务错误。该 Call 使用上游消息状态转换，gasUsed 包含 intrinsic gas；它不收取手续费、不提交状态，也未冒充完整 Ethereum JSON-RPC。

原生回执的 `blockHash` 使用零值，避免执行回执与自定义区块 ID 产生循环承诺；真实包含块由框架交易/回执索引的 `blockId` 提供。没有 Shanghai/Cancun/Prague 等后续 fork、blob、提现或 Beacon 系统调用。

## 多节点、恢复与边界

复制预设，为各节点设置独立数据库、身份目录和 API/P2P 端口；保持模型配置、创世、共识和 chain ID 一致。将已启动节点打印的 `/p2p/...` 地址加入其他节点 `network.config.seeds`。观察节点可配置 `consensus.config.mining: false`，通过相同验证器同步区块。

重组以各父块快照重新执行并切换 canonical 索引；脱离主链交易可回到池中。停止后重启保留确认交易、回执和状态，离线节点重新连接后追赶。

当前存储仍是开发规模的完整状态快照。EVM 原生 KV 镜像限 64 MiB，单交易限 1 MiB，block Gas 上限可配置 21000–100000000；原生 trie 历史数据尚未做裁剪。达到容量上限会明确失败，不应视为生产级大链方案。模型验证不等于 BTC/ETH 主网协议兼容。

验证命令：

```sh
go test ./model/bitcoin ./model/evm ./api/http/native
go test ./assembly -run TestNativePresetHTTPSubmitAndReopen
go test ./protocol/composed -run TestNativeChainsReorgNetworkAndRecovery
```

后两个测试使用真实 PluginKit 配置、HTTP、Bolt 和 libp2p，覆盖转账确认、回执、重启续转、双节点状态一致、分叉回滚和离线追赶。
