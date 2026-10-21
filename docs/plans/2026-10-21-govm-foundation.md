# GOVM 首批能力实施计划

**Goal:** 实现已批准的 Journal、真实签名/验签、静态成员集和可组合签名 PoA，VM 保持独立仓库。

**Architecture:** core 不变；消费方使用 cap 和类型化 Deps。Journal 是执行局部能力，签名者不参与验块，成员提供者不依赖 Ledger；PoA 规则、证明生产、调度、选链分离。沿用当前开发分支并保留已审核文档，不另建与当前变更脱节的工作区。

**Tech Stack:** Go 标准 Ed25519、现有 PluginKit/keystore、composed 协议与分支账本/libp2p。

采用 subagent-driven-development：每批先写失败测试、最小实现、规格审查、独立质量审查，再进入下一批；不会自动提交或推送。

## 执行记录

- Task 1 已实现并通过规格、独立质量审查；局部 race 与 vet 通过。Journal 仅提供执行局部 KV 隔离，不包含持久化、事件或 VM。资源预算包括所有尚未释放的 checkpoint 快照。
- Task 2 已实现并通过规格、独立质量审查；签名/验签、密钥适配和成员插件的局部 race 与 vet 通过。
- Task 3 已实现并通过规格、独立质量审查；局部 race 与 vet 通过。独立 CLI 测试已验证随机密钥初始化、真实本机网络/HTTP、签名出块及磁盘重开后从高度 1 继续到 2。
- Task 4 已实现三节点真实网络与 HTTP 转账闭环，规格与整体独立质量审查通过；最终全仓 race、vet、格式与 diff 检查通过。停机测试助手严格保留非空故障，超时不继续重开数据库。首批实现已完成，提交与推送按用户指令执行；后续 GOVM 阶段未包含在本批中。

## Task 1 Journal 与窄读取适配

创建 cap/state.Reader、Factory，Journal 嵌入 Reader；实现 state/journal 的执行局部 overlay、显式删除、LIFO checkpoint、Revert/Release、Changes 与插件 state/journal。基线借用不关闭，core.StateReader 适配只转换纯 ErrNotFound，保留其他故障树。写入、返回值、检查点快照均隔离；非 LIFO、失效及外来 token 不改状态。配置合理资源预算，取消及预算失败不留下半次修改。

先测：缺失/空值/nil、覆盖/删除/恢复、嵌套 Release 后外层回滚、Changes 所有权、外来/过期 token、基线故障及混合错误、取消、预算、真实存储快照适配。再实现并执行相关包 race/vet，完成双审。

## Task 2 签名套件与静态成员集

实现明确版本的 Ed25519 套件：域与 payload 的唯一二进制 framing、公钥/私钥长度校验、完整域非空、正确 suite 才可验签。签名者通过 Deps 获取明确的本地密钥；验签者不依赖私钥。构造无 IO，不隐式生成测试密钥；keystore 签名适配在使用时读取材料并尽力清理。

修订尚无消费者的 cap/validator 为 Identity + Members(ctx,parent)；实现冻结静态成员提供者，配置版本/顺序/ID/公钥/suite 全部绑定 Identity，Set.Anchor 绑定父块，Set.ID 绑定成员集合。无 Ledger/Protocol 依赖环。

先测：独立标准库验证/固定向量、错误域/链/用途/版本/公钥/签名/套件、非法私钥、owned bytes、取消与 keystore 故障。成员集测深复制、顺序和配置绑定、重复成员/公钥拒绝、未知 suite 明确拒绝消费。PluginKit 真实装配与多实例隔离；race/vet 后双审。

## Task 3 PoA 规则与独立调度

实现 rules/poa（只读规则）、choice/authority（独立分叉排序）、authority sealer（持有 Signer）、consensus/authority（监督出块与同步）。必要的封印协议适配可在 composed 增加可选 wrapper，不给所有协议加签名方法；验块不读取本地私钥。

slot/epoch/间隔/成员绑定进语义 ID 与创世，候选生产者及 slot 由规则校验；Draft 只由给定父块和头确定，禁止读取时钟或 Signer 改写共识数据。缺席允许后续合法 slot，空交易块仍需签名。签名绑定执行定稿后的完整头、链实例和用途；证明不改变既有 Block.ID 规则。Choice 不假装提供 BFT 最终性，等分有明确稳定比较规则。

保留纯取消与冲突/非本节点轮次的可重试语义；未知、混合或已提交维护故障必须向根节点报告。producer、importer、ledger 恢复都执行完整规则；本地时钟只用于出块调度，不作为历史验块随机输入。共享运行生命周期遵守现有监督和清理约束，不为 PoA 复制 PoW 挖矿逻辑。

测试先覆盖纯规则、签名替换、跨链重放、错误成员/轮值/slot、无签空块、确定性 Draft/候选上下文与恢复，然后验证 PoA × account/UTXO 转账、分叉重组与重开。提供可运行单节点开发 preset 及多节点配置说明，无内置公开测试私钥。网络节点身份与共识签名密钥分开。

## Task 4 组合交付与真实进程验证

三节点真实 libp2p 同步与轮值、缺席 slot、分叉重连和停机/重启追赶；至少一次 PoA HTTP 已签交易提交/确认/查询/持久化闭环。使用临时目录和测试注入口令/密钥，用户既有数据不改。

全仓 go test -race ./...、go vet ./...、gofmt、git diff --check；独立最终审查。更新能力状态与运行说明：本批没有 GOVM 引用块、动态成员、跨片证明、钱包/管理 API 或真实外部 VM；后续阶段仍未完成。
