# GoChainKit

基于 Go 和 PluginKit 的模块化区块链开发框架，目标是通过配置装配模块，快速构建一条新链。

当前支持配置驱动的独立节点 CLI、单节点出块、事务落盘与重开恢复。内置 single 与简化 PoW 两种开发共识；PoW 可通过 libp2p 组成多节点开发网络。

接口分层采用稳定的 `core` 与可演进的 `cap/<能力>`；领域实现和插件通过类型化 Deps 组合。参见 [能力分层与实现状态](docs/capabilities.md)。

## 已实现

- 格式无关的交易、区块、ID 和状态承诺接口，以及可注入的协议编解码、构造与验证能力。
- 默认 KV 协议的签名、ID 和状态根算法，隔离在 protocol/kv。
- 不透明收据载荷与通用确认交易记录，不强制默认区块头或账户模型。
- 有界内存交易池及 PluginKit 注册，支持去重和复制隔离；selector/fifo 子插件负责预算选取。
- 构块插件、单节点定时共识和提交协调器。
- 最小键值执行器、内存状态/区块/回执存储，支持链头推进。
- 真实插件图装配与出块集成测试。
- 可选 PoW 协议、分支存储与累计工作量选链，支持原子重组及重开恢复。
- libp2p 网络：持久身份、种子重连、有界消息及协议/创世隔离。
- bbolt 持久化插件：区块、状态、已确认交易、回执和链头在同一事务落盘，支持重开恢复。
- 可组合 HTTP API：协议交易受理、链/区块/确认交易/回执查询、节点信息，以及按需装配的账户与 UTXO 查询。
- 事务型 KV 后端：通用账本通过 Deps 选择内存或 Bolt；密钥保存与网络身份独立装配，支持加密文件及环境口令来源。

## 开发

需要 Go 1.26.1，与当前 PluginKit 版本要求一致。

```sh
go test ./...
go test -race ./...
go vet ./...
go test ./tests/integration -v
```

## 设计文档

- [架构草图](docs/plans/2026-10-17-gochainkit-design.md)
- [编程与模块接口规范](docs/development-conventions.md)
- [实现计划](docs/plans/2026-10-17-gochainkit-implementation.md)
- [持久化存储](docs/storage.md)
- [密钥保存与身份适配](docs/key-management.md)
- [模块与协议接口](docs/interfaces.md)
- [外部 API、插件装配与资源限制](docs/api.md)
- [libp2p 与 PoW 开发网络](docs/network-consensus.md)
- [架构评估与下一步建议](docs/architecture-review.md)

后续按需求扩展外部访问能力与持久化压力验证。

当前状态根算法扫描完整快照；内存存储保留历史快照，适用于小规模开发验证。永久无效交易会从池中驱逐，暂不可执行交易保留并跳过；链头冲突会重试，内部执行与存储错误仍向运行器报告。

协议化接口与旧固定格式 API 不兼容。Bolt 使用格式 2，并核对协议与创世绑定；旧格式 1 数据库会被拒绝，未提供自动迁移。

## 启动开发链

```sh
go run ./cmd/gochainkit --config presets/single.yaml
# 有界运行后自动关闭；数据库位于进程工作目录的 data/chain.db
go run ./cmd/gochainkit --config presets/single.yaml --run-for 5s
```

默认配置通过 PluginKit 共享一个 Ledger，根 node/default 负责账本与资源的启动、回滚、关闭和任务监督。重复启动命令会打开原账本并继续出块。API 地址随启动打印，客户端提交已经签名的协议交易；受理不代表确认。自定义程序可自行选择插件包，不必导入全部内置实现。详见 [节点运行](docs/node-runtime.md) 和 [外部 API](docs/api.md)。

## PoW 开发网络

```sh
go run ./cmd/gochainkit --config presets/pow.yaml
```

启动后打印可用于种子连接的完整节点地址。为其他节点复制配置，使用独立数据目录、密钥保存目录和监听端口，并将该地址加入 network 的 seeds。设置共识配置 mining: false 可只同步不挖矿。详见 [网络操作与验证边界](docs/network-consensus.md)。

PoW 预设使用加密身份，运行前须通过环境提供 `GOCHAINKIT_KEYSTORE_PASSWORD`；口令不写入 YAML。旧明文 identityPath 不会自动迁移，保留原 PeerID 的导入方式见 [密钥保存](docs/key-management.md)。single 配置不需要口令。
