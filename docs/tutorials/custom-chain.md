# 构建自己的链：两条扩展路径

先运行 [第一笔交易](first-transaction.md)，再选择替换范围。本页是接口接入路线，不是一套已提供的新链生成器；下列片段只说明装配变化，完整运行配置以 presets 为基线。

## 路径 A：自定义 Protocol + Executor

适合需要独立交易/区块格式、ID/承诺算法或引用语义的链。无需继承 KV 交易或 composed Header。

1. 在应用自己的 Go module 定义实现 `core.Transaction`、`core.Block` 的不可变对象；ID 是不透明字节字符串。
2. 实现 `core.Protocol` 中的小能力组合：交易/区块 codec、无状态校验、Genesis/Draft/Finalize、候选与完整区块校验、StateCommitment、ProtocolDescriptor。规范字节必须确定；ID、描述符与真实创世绑定需一致。
3. 实现 `core.Executor`，在借用的隔离父状态上返回 `ExecutionResult`，不直接提交。绑定 Parent/BlockID，明确 Writes/Deletes、回执与确定性错误。
4. 用应用自己的薄适配注册独立 Kind，通过 Config/Deps 注入；交易池、selector、builder、ledger、host、API 共同引用正确协议/同一账本 owner。
5. 从 `presets/single.yaml` 复制配置，替换 protocol/executor 的 `use` 与参数。若需要分支同步，额外实现其能力契约；只实现 core.Protocol 不等于自动满足 BranchProtocol。
6. 最终程序显式导入所需插件，使用公开 `assembly.Load(ctx, reader)` 得到 Node，处理错误与 build 结果后调用 `Node.Run(ctx)`。Node 生命周期见 [运行指南](../node-runtime.md)，不要手工重复打开同一 Ledger。

验证证据可阅读仓库的 `internal/testprotocol` 与 `tests/integration/production_test.go`，但外部模块不能导入 internal 包。教程中的扩展必须在自己的 module 实现/注册，不能将测试替身宣称为公开 SDK。

## 路径 B：composed 的 Engine + Rule + Choice

适合接受 composed 封装族，仅替换业务状态语义或共识策略的链。

- `cap/model.Engine`：描述符、交易 codec/准入、创世状态、Execute；可选 ContextualEngine 取得确定性 Parent/Candidate/History。不强制资产、nonce 或手续费。
- `protocol/composed.Rule`：ID、Genesis、Draft、Validate，规则语义参数须绑定身份。Draft 不重新读取本机时钟/随机数或替换生产者。
- `protocol/composed.Choice`：ID、Score、Prefer、Finalized；score 是不透明字节，只能由策略比较。
- 领域入口：`composed.New(config, engine, rule, choice)`；工作量组合使用 `composed.NewPoW` 并要求 WorkRule。
- 执行入口：`composed.NewExecutor(protocol, history)`；不能调用已移除的 Protocol.Execute/Base 桥接。

例如已有账户 PoW preset 的依赖片段：

```yaml
model:
  use: model/account
  config: {Network: composed-account}
rule:
  use: rules/pow
  config: {retargetInterval: 20, targetSpacing: 10, initialReward: 50, halvingInterval: 100}
protocol:
  use: protocol/composed-pow
  config: {network: composed-account, genesisTime: 1700000000, producer: '0300000000000000000000000000000000000000000000000000000000000000'}
  deps: {model: model, rule: rule, choice: rule}
```

这是 `presets/pow-account.yaml` 的片段，不可独立启动。rule 在该例同时实现规则和 Choice，不表示所有实现必须合并。自定义非资产 Engine 可关闭 Economics；启用奖励/费用的组合才需要 Currency，不能向任意模型强加余额。

封印在执行定稿之后；builder 校验提案，持久化完整验证封印后的区块。PoA 需要真实父块的 sealer 和静态成员适配，详见 [PoA 指南](../govm-foundation.md)，不能把 PoW sealer 直接替换名称当作任意共识。

## 两条路径共同的交付条件

同一输入在本地生产、网络导入、侧链重放和恢复中结果一致；缺必要能力在构造/启动时拒绝；描述符相同不代表账本 owner 相同。覆盖错误分类、复制所有权、取消、原子提交、重组/恢复和跨配置旧库拒绝，见 [测试清单](../contributing/testing.md)。

变更编码、签名、创世或共识规则需要协议演进决策，见 [兼容与恢复](../guides/compatibility-recovery.md)。DAG/分片、真实 Runtime 和动态验证者均需额外设计，不是上述路径的隐含功能。
