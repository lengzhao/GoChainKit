# 模块与协议接口

GoChainKit 的通用组件依赖接口与协议能力，不规定具体区块头、交易字段、签名算法、ID 长度或状态承诺算法。当前公共定义见 core.go。

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
- BlockValidator：验证具体协议的引用关系、执行绑定和承诺；可以查询 ChainStore 检查额外引用。
- StateCommitter：计算具体协议的状态承诺。
- ProtocolBound：返回协议名称、版本、网络描述符。

编码必须确定；解码结果重新编码后必须得到相同的规范字节，ID 也必须保持不变。返回对象与字节由调用方拥有，不能引用可变的全局对象。具体协议负责输入大小、字段、签名域和算法约束。

ProtocolDescriptor 用于拒绝显然不匹配的依赖组合，不替代协议正确性证明。实现改变共识语义时必须调整版本或相关身份，不能仅因接口编译通过就认定相容。

## 构块 执行与提交

1. Mempool 通过协议验证和编码交易，保存独立字节副本，按编码长度限制容量。
2. BlockBuilder 读取当前 head 对应对象，经协议 Draft 生成候选，不能直接填写某个默认区块头。
3. Executor 在隔离 StateReader 上运行草稿，返回通用 Writes、收据载荷和状态承诺；Parent 绑定本次读取的 canonical cursor，BlockID 绑定草稿 ID。
4. 协议 Finalize 将执行结果纳入具体格式。构块器把 ExecutionResult.BlockID 重新绑定为最终区块 ID，再验证提议。
5. Coordinator 在提交前重新执行和验证，不信任缓存执行结果。
6. 存储核对写入集对应的状态承诺与协议验证结果，在原子重查 head 后一起保存数据，成功后交易池才移除交易。

ExecutionResult 中的 Parent 是执行基线，不是要求具体区块只能有一个父引用。自定义协议可以保存额外引用并在 ValidateBlock 中检查。

## 存储与恢复

存储使用协议编码字节保存区块和交易，不序列化 Go 接口值，不依赖具体结构体。确认交易记录只提供 Transaction、BlockID 和块内 Index，高度等元数据由协议解析。

StateReader.Root 返回不定长 Commitment。收据使用 TransactionID 与协议定义 Data，当前后端支持每交易最多一份聚合收据，也允许协议完全不产生收据。

Bolt 格式 2 保存协议身份与创世绑定。打开时拒绝身份、创世或格式不匹配；不会自动迁移或覆盖旧数据库。启动恢复验证完成前不向业务调用发布数据库。

## 默认协议

原固定类型已移到 protocol/kv/types，签名、SHA-256、默认区块头和序号规则只存在于默认协议及其执行器。protocol/kv 提供私有不可变包装和显式 Wrap/Unwrap 函数；plugins/protocol/kv 负责 PluginKit 注册。

配置图中的 protocol/kv 实例由交易池、执行器、构块器、存储与协调器共同引用。通用组件在构造时检查依赖描述符；KV 专属执行器拒绝其他协议。

## 自定义链接入

定义自己的 Transaction、Block，实现配套 Protocol 与 Executor，再通过 PluginKit 将它们注入通用模块。无需继承默认 KV 交易或区块头。

internal/testprotocol 提供测试样例：自定义 Label/Body 交易，无签名和 nonce，使用带前缀的二进制 SHA-512 标识、自定义区块字段与附加引用、64 字节状态承诺。tests/integration 验证它通过同一套交易池、构块、协调和 Bolt 后端完成出块、落盘、重开与继续提交。

## 当前能力边界

默认存储提供单一 canonical cursor，状态为键值快照和写入集；多头链、删除语义、特殊状态树或其他共识算法可能需要扩展能力或替换相应插件。自定义区块格式不等于已经实现 GOVM 的多链协议，也不意味着任意共识算法可以互换。

这些边界属于当前具体组件能力，不应通过给所有交易、区块添加默认字段来解决。
