# 模块与协议接口

GoChainKit 的通用组件依赖接口与协议能力，不规定具体区块头、交易字段、签名算法、ID 长度或状态承诺算法。稳定基础定义见 core/contracts.go；分支、共识、网络、复制、模型及资产的可选契约见 cap 对应子包，不保留旧路径别名。详见 [能力分层](capabilities.md)。

## 对象与标识

```go
type ID string
type Commitment string
type Transaction interface { ID() ID }
type Block interface { ID() ID }
```

ID 和 Commitment 是不可变的不透明字节字符串，不意味着十六进制或 32 字节。ID 必须稳定、非空，并在相应对象命名空间中唯一；对象生成后不能修改影响 ID 的内容。API 展示和网络编码由具体协议决定，含非法 UTF-8 的值应按字节或显式编码保存。

框架不要求交易存在 nonce、公钥、账户、手续费或键值操作，也不要求区块存在固定 Header、线性 Parent 或某种状态根字段。

## 协议能力

Protocol 由一组较小能力组成，开发者可以组合实现：

- TransactionCodec：交易编码与解码。
- TransactionValidator：无状态准入检查；状态相关验证仍属于 Executor。
- BlockCodec：区块编码、解码与交易提取。
- BlockFactory：创世、草稿和根据执行结果定稿。
- CandidateValidator：执行前核对候选绑定当前 canonical cursor，旧候选返回 ErrConflict；格式错误不能伪装成链头冲突。
- BlockValidator：验证具体协议的引用关系、执行绑定和承诺；可以查询 ChainStore 检查额外引用。
- StateCommitter：计算具体协议的状态承诺。
- ProtocolBound：返回协议名称、版本、网络描述符。

编码必须确定；解码结果重新编码后必须得到相同的规范字节，ID 也必须保持不变。返回对象与字节由调用方拥有，不能引用可变的全局对象。具体协议负责输入大小、字段、签名域和算法约束。

ProtocolDescriptor 用于拒绝显然不匹配的依赖组合，不替代协议正确性证明。实现改变共识语义时必须调整版本或相关身份，不能仅因接口编译通过就认定相容。

## 构块 执行与提交

1. Mempool 通过协议验证和编码交易，保存独立字节副本，按编码长度限制容量。
2. TransactionSelector 从 Mempool.Candidates 的副本中按预算选择；BlockBuilder 读取当前 head 对应对象，经协议 Draft 生成候选，不能直接填写某个默认区块头。
3. Executor 在隔离 StateReader 上运行草稿，返回通用 Writes、收据载荷和状态承诺；Parent 绑定本次读取的 canonical cursor，BlockID 绑定草稿 ID。
4. 协议 Finalize 将执行结果纳入具体格式。构块器把 ExecutionResult.BlockID 重新绑定为最终区块 ID，再验证提议。
5. Coordinator 先调用 ValidateCandidate，再重新执行和验证，不信任缓存执行结果。
6. 存储核对写入集对应的状态承诺与协议验证结果，在原子重查 head 后一起保存数据，成功后交易池才移除交易。

ExecutionResult 中的 Parent 是执行基线，不是要求具体区块只能有一个父引用。自定义协议可以保存额外引用并在 ValidateBlock 中检查。

## 存储与恢复

存储使用协议编码字节保存区块和交易，不序列化 Go 接口值，不依赖具体结构体。确认交易记录只提供 Transaction、BlockID 和块内 Index，高度等元数据由协议解析。

StateReader.Root 返回不定长 Commitment。收据使用 TransactionID 与协议定义 Data，当前后端支持每交易最多一份聚合收据，也允许协议完全不产生收据。

Bolt 格式 2 保存协议身份与创世绑定。打开时拒绝身份、创世或格式不匹配；不会自动迁移或覆盖旧数据库。启动恢复验证完成前不向业务调用发布数据库。

## 默认协议

原固定类型已移到 protocol/kv，签名、SHA-256、默认区块头和序号规则只存在于默认协议及其执行器。protocol/kv 提供私有不可变包装和显式 Wrap/Unwrap 函数；plugins/protocol 负责 PluginKit 注册。

配置图中的 protocol/kv 实例由交易池、执行器、构块器、存储与协调器共同引用。通用组件在构造时检查依赖描述符；KV 专属执行器拒绝其他协议。

## 自定义链接入

定义自己的 Transaction、Block，实现配套 Protocol 与 Executor，再通过 PluginKit 将它们注入通用模块。无需继承默认 KV 交易或区块头。

internal/testprotocol 提供测试样例：自定义 Label/Body 交易，无签名和 nonce，使用带前缀的二进制 SHA-512 标识、自定义区块字段与附加引用、64 字节状态承诺。tests/integration 验证它通过同一套交易池、构块、协调和 Bolt 后端完成出块、落盘、重开与继续提交。

## 当前能力边界

默认存储提供单一 canonical cursor，状态为键值快照、显式写入与删除集；多头链、特殊状态树可能需要扩展能力或替换相应插件。自定义区块格式不等于已经实现 GOVM 的多链协议，也不意味着任意共识算法可以互换。

这些边界属于当前具体组件能力，不应通过给所有交易、区块添加默认字段来解决。

## 依赖与错误契约

消费者使用 TransactionProtocol、BuilderProtocol、ExecutionProtocol、CoordinatorProtocol 与 LedgerProtocol 能力组合。默认协议可一次实现全部能力；不同消费者无需依赖完整 Protocol。

Ledger 汇总读、状态与原子提交能力；LedgerIdentity 是进程内账本 owner，不是协议或网络身份。构块器、协调器与根节点必须绑定同一 owner，相同协议的不同账本不能混装。存储内部在写事务中重查 head，前置校验不能替代 CAS。

TransactionError 必须指明当前候选中的交易 ID 和 Rejected/Deferred。构块器有限次重建候选，Rejected 驱逐、Deferred 本轮跳过但保留；无效错误分类或内部错误直接上报。单节点共识对 ErrNoProposal、ErrConflict 重试；CommittedError 表示已提交、清理失败，不能作为未提交重试。

交易选择是独立子插件；状态树、虚拟机、网络与分布式共识保留替换方向，当前没有对应生产实现。

## 可选工作量与分支能力

BranchProtocol 是可选单父分支能力，与通用Protocol的任意引用格式分离；BranchPolicy独立提供不透明Score、Prefer选链和Finalized最终性，并绑定策略配置身份。WorkProtocol提供Parent、Work和BlockSealer，旧工作量规则自动适配为策略。BranchLedger.Import保存已验证的分支并返回主链切换中的Attached/Detached，存储独立阻止跨最终确认点的重组。通用 Protocol 与 Block 不因此要求 PoW 字段，原单节点 KV 组合继续可用。Network 仅提供有界通信与生命周期能力，不暴露 libp2p 的 host/peer 类型。

## 执行定稿与共识封印

`BlockFactory.Finalize` 绑定执行承诺。提供可选 `ProposalValidator` 的协议可让构块器验证尚未封印的提案；之后共识通过 `BlockSealer` 生成证明。持久化始终调用 `ValidateBlock`，未封印提案不能落盘。未提供新能力的旧协议仍按原定稿后验证路径运行。

状态变更的 `Writes` 与 `Deletes` 分开表达，空值仍表示存在的键。`core.ApplyStateChanges` 提供统一、复制隔离的应用语义；删除重复或与写入冲突明确拒绝。


## 实现分层与可复用运行能力

KV 执行语义由 `protocol/kv.NewExecutor` 提供，`plugins/executor` 仅适配注册。`protocol/powkv.NewExecutor` 显式接收内层 KV 执行器并验证其协议身份，不再引用插件层或隐式创建依赖。旧 KV/powkv 格式保持不变。

composed 的执行统一通过 `composed.NewExecutor(protocol, history)` 创建；Protocol 本身不再实现 Executor，也不公开 Base 具体类型桥接。模型上下文、奖励应用和执行结果整理归入独立 Executor，普通模型和上下文模型使用同一入口。

严格 JSON 解码 mechanics 复用 `internal/jsoncodec`；每种协议继续决定大小上限、是否要求规范字节及签名和交易语义。KV 保留原有严格解码策略，不能因公共助手而改变历史编码接受规则。


`cap/replication.BlockImporter` 是验证、执行、分支提交及提交后交易池维护的独立能力，`cap/replication.Replicator` 是链追赶与交易传播的独立运行能力。两者都绑定协议和账本 owner。`importer/branch` 与 `sync/chain` 通过依赖图装配；PoW 调度器只依赖构块、封印和这些运行能力。同步插件拥有唯一网络消息 handler，不要求工作量证明能力。

Memory/Bolt 共享包内导入准备逻辑，后端分别持有原子提交和 CAS。正常导入只加载执行父块与规范链头的相关祖先，规范链交易和回执索引根据 Detached/Attached 增量更新；启动恢复仍全量核对。深链祖先遍历和全状态快照成本仍存在，不能据此承诺常数复杂度。

## 扩展能力入口

模型执行契约统一位于 `cap/model`，不保留旧 model 包接口别名。新增可选能力位于 `cap/<能力>`，具体接口状态、消费者和缺失能力处理见 [能力分层](capabilities.md)。这些草案不意味着已有 PoA、合约或引用体运行实现。
