# 框架目标与边界

GoChainKit 是基于 Go/PluginKit 的模块化区块链开发框架，通过显式类型化依赖和配置组装新链。它提供可复用能力与参考实现，不保证任意组件可无条件互换，也不把开发级共识宣称为生产安全协议。

## 分层

1. `core`：格式无关对象、协议小能力、执行/账本及错误契约；Block/Transaction 只要求稳定 ID。
2. `cap/<能力>`：可选扩展契约，不反向依赖具体插件；部分接口仍是草案，见 [状态矩阵](status.md)。
3. 领域实现：`protocol/kv`、`protocol/powkv`、`protocol/composed` 以及 model、rules、choice、state、storage 等；链格式和策略在这一层定义。
4. `plugins`：注册、Config/Deps 与装配适配；`assembly` 调用 PluginKit；根 Node 负责资源生命周期和运行监督。

```text
Model + Rule + Choice → composed Protocol → Executor / Builder
Protocol + Ledger + Executor + Pool → Importer
Network + Ledger(read-only) + Importer + Pool → Replicator
Builder + Sealer + Importer + Replicator → Consensus runtime
Node → 统一启动、监督、取消与资源释放
```

这是现有组合族的一种路径，自定义协议无需使用 composed Header，也不必实现资产或 PoW 能力。详见 [两条扩展路径](tutorials/custom-chain.md)。

## 核心名词

- Protocol 决定规范字节、对象身份、承诺及验证；协议描述符相同只是兼容性初筛。
- Engine/Executor 决定状态转换；Rule 校验共识规则；Sealer 生成证明；Choice 决定选链和最终性。
- Ledger owner 表示同一进程内账本资源，不能用“相同网络/协议”代替同一 owner。
- accepted 是本地池受理，confirmed 是一次 canonical 观察；均不自动等同不可逆最终性。
- Journal 是执行局部隔离；Release 不是持久提交。当前 KV Journal 不等于完整 Host/事件回滚。

## 当前边界

core 不强制 32 字节 ID、账户、nonce 或单父块格式；但现有账本使用 canonical cursor 与 KV 快照，分支同步采用单父能力。DAG、多头状态、多分片和跨链需要各自真实实现，不能只靠最小接口声明支持。

静态单签 PoA 不是 DPoS 或 BFT。简化 powkv 与规则级 PoW 都不是 Bitcoin 格式/P2P 兼容实现。内置存储保留完整快照，未给出大链性能承诺。外部 VM、完整合约调用与 Router/引用体尚未接通。

接口兼容承诺、生产目标和 DPoS/VM 优先级仍待确定，见 [路线图](roadmap.md)。
