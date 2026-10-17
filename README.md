# GoChainKit

基于 Go 和 PluginKit 的模块化区块链开发框架，目标是通过配置装配模块，快速构建一条新链。

当前支持配置驱动的独立节点 CLI、单节点出块、事务落盘与重开恢复。默认共识仅供开发，不提供多节点一致性。

## 已实现

- 格式无关的交易、区块、ID 和状态承诺接口，以及可注入的协议编解码、构造与验证能力。
- 默认 KV 协议的签名、ID 和状态根算法，隔离在 protocol/kv。
- 不透明收据载荷与通用确认交易记录，不强制默认区块头或账户模型。
- 有界内存交易池及 PluginKit 注册，支持去重和复制隔离；selector/fifo 子插件负责预算选取。
- 构块插件、单节点定时共识和提交协调器。
- 最小键值执行器、内存状态/区块/回执存储，支持链头推进。
- 真实插件图装配与出块集成测试。
- bbolt 持久化插件：区块、状态、已确认交易、回执和链头在同一事务落盘，支持重开恢复。

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
- [模块与协议接口](docs/interfaces.md)
- [架构评估与下一步建议](docs/architecture-review.md)

后续按需求补充 HTTP API，并扩展持久化压力与兼容性验证。

当前状态根算法扫描完整快照；内存存储保留历史快照，适用于小规模开发验证。永久无效交易会从池中驱逐，暂不可执行交易保留并跳过；链头冲突会重试，内部执行与存储错误仍向运行器报告。

协议化接口与旧固定格式 API 不兼容。Bolt 使用格式 2，并核对协议与创世绑定；旧格式 1 数据库会被拒绝，未提供自动迁移。

## 启动开发链

```sh
go run ./cmd/gochainkit --config presets/single.yaml
# 有界运行后自动关闭；数据库位于进程工作目录的 data/chain.db
go run ./cmd/gochainkit --config presets/single.yaml --run-for 5s
```

默认配置通过 PluginKit 共享一个 Ledger，根 node/default 负责账本与资源的启动、回滚、关闭和共识任务监督。重复启动命令会打开原账本并继续出块；此版本尚无外部交易提交 API。自定义程序可自行选择插件包，不必导入全部内置实现。详见 [节点运行](docs/node-runtime.md)。
