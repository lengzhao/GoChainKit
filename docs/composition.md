# 共识规则与账本模型组合

`protocol/composed` 是一种可选的协议封装族，不改变 core 的最小 Block/Transaction 接口。交易与状态规则由 `model.Engine` 提供；`Rule` 决定共识头、提案与证明验证；`Choice` 决定分数、选链和最终性。它们是独立依赖，不按模型名称或共识枚举分支。

## 封装和执行

Header承诺交易、状态、回执及不可变Consensus字节；块ID为该Header的SHA256d。独立Proof旁证不参与ID，允许相同提案携带不同合法证书集合。PoW nonce/target属于Header.Consensus，参与ID；PoW规则不接受旁证。该结构仅约束本封装族，其他自定义协议无需沿用。

构块器先执行，再Finalize绑定状态与回执承诺，并通过ProposalValidator检查尚未封印的提案。PoW运行器随后显式Seal、重新绑定ExecutionResult.BlockID，再完整验证和持久化。旧powkv的Seal保持幂等，原组合继续可用。

普通Engine无余额、nonce、高度或手续费要求。可选ContextualEngine通过独立composed.Executor获得Parent、Candidate及只读History，避免协议与账本构造依赖环。具体context另提供StateCommitment能力，便于模型包装器生成正确的中间快照；基础ExecutionContext不增加强制状态布局。执行视图的Candidate由原交易/时间/生产者确定性重建，清空执行承诺及封印后的nonce，确保初次执行与网络重放看到相同ID和头；Parent及History.Block剥离可变证书旁证，History.Head固定执行父块。Rule.Draft必须由父块与传入头确定，不能重新取本地生产者或时间。具体context另提供Height和Timestamp可选能力。模型History只暴露区块查询能力，不暴露canonical Receipt索引；Block查询限于执行父块的祖先，本地额外侧链统一返回NotFound，避免节点间可见性差异影响状态。需要分支回执时应增加独立ReceiptAt能力。

## 规则级PoW

`rules/pow`提供规范compact target编码和合法性校验、SHA256d验算、从已验证祖先推导预期目标、周期调难、四倍上下限、目标上限、MTP和未来时间偏移校验，以及真实累计工作量选择。同工作量保留当前主链，后续区块可打破平局。

默认调难周期2016、间隔600秒、MTP窗口11、未来偏移7200秒；默认开发目标0x207fffff仅用于快速验证。开发环境可缩短周期和间隔。规则身份包含全部语义参数；组合创世绑定规则身份、选链身份、模型描述与真实创世状态、网络和创世时间。生产者收款地址是节点本地参数，不改变链身份。

这是使用自定义区块/交易/网络编码的Bitcoin式PoW规则，不与比特币节点互通。尚不提供Bitcoin脚本、交易格式、coinbase成熟期或timelock；奖励可以立即使用。当前状态快照、分支图和历史调难查询以开发规模为目标，未作大链性能承诺。

## 可选资产适配

只有启用Economics的规则才要求asset.Currency；组合执行器按模型FeeEffect取得手续费，按规则计算奖励，再通过Currency.Credit应用系统变更。金额相加检查溢出，系统mint标识绑定创世、父块、高度和生产者，状态变更统一应用显式删除语义。

InitialReward大于零或currency为true会启用资产适配；currency=true可用于零区块奖励但收取手续费的链。普通非资产计数模型可使用关闭Economics的PoW规则，已有独立回归证明其不需要余额或UTXO。

## 配置及验收

见presets/pow-account.yaml和presets/pow-utxo.yaml。它们通过同一规则插件分别装配账户、UTXO模型，共用存储、构块、网络与共识运行器。producer使用32字节收款地址的十六进制文本。两个示例使用不同网络标识和磁盘路径；多节点运行同一组合时须保持网络、模型创世分配和规则配置一致，分别设置本地磁盘、身份、监听地址和种子。

已覆盖两种模型的真实签名转账、奖励与手续费、分叉重组、被撤销转账恢复、Bolt重开并继续出块；真实libp2p测试覆盖交易传播、对端确认、节点停机后继续出块及重启追赶的状态一致。另有非货币计数模型与可选执行上下文测试，以及compact非法编码、历史目标伪造、调难限幅、MTP、未来时间、奖励减半和挖矿取消检查。
