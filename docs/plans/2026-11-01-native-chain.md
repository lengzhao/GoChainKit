# Native Chain Execution Implementation Plan

**Goal:** 将 BTC/ETH 原生交易能力接入完整自定义链执行与节点流水线。

**Architecture:** 新增 model/bitcoin 和 model/evm；原生算法由上游库实现，适配器连接不可变父状态、候选块上下文与模型 Transition。通过既有 composed 节点组装。

**Tech Stack:** btcd v0.26.0、wire/txscript v2、go-ethereum v1.17.10、PluginKit、Bolt。

### Task 1: BTC 执行模型
- 新建 model/bitcoin/engine.go、engine_test.go。
- 先测试真实签名花费、金额与双花拒绝、锁定、批内依赖和失败隔离，再实现模型。
- 原生交易包装仅冻结 wire bytes 和 txid，不改变编码。
- 用 go test ./model/bitcoin 验证。

### Task 2: EVM 执行模型
- 新建 model/evm/engine.go、engine_test.go。
- 先测试转账/nonce/gas、合约部署/存储/revert、状态持久化映射与隔离。
- 复用上游 StateDB 和状态转换，使用独立内存 KV 镜像连接现有快照。
- 用 go test ./model/evm 验证。

### Task 3: 装配及查询
- 注册 model/bitcoin 与 model/evm，提供各自 PoW preset。
- 原生状态查询与只读 EVM 调用走独立模型路由插件。
- 测试实际 PluginKit 构图、交易出块、Bolt 重开、分支重组和多节点同步。

### Task 4: 审查与验证
- 先检查规格覆盖，再审查错误、确定性及隔离。
- go test ./...；相关 go test -race；go vet ./...；真实配置有界运行。
- 文档记录已验证闭环、原生规则 profile 与仍存边界。
