# GOVM 能力接口与插件组合设计草案

状态：2026 年 10 月 21 日用户确认按设计推进，首批实现 Journal、Ed25519 签名/验签、静态成员集和签名 PoA；后续阶段仍按下列顺序推进。设计日期：2026 年 10 月 20 日。

目标：以 GoChainKit 组合出具备 GOVM 目标能力的链。框架提供能力契约和公共插件，独立 VM 仓库实现语言与执行引擎；复杂功能只通过类型化 Deps 组合子能力。

源码基线与逐项能力差距见 [功能盘点](../govm-feature-map.md)。本设计不要求旧磁盘、网络和交易编码兼容；不以旧代码中返回 nil 或占位输出的路径作为验收。

## 方案选择

采用按能力逐段接通：复用已有 cap，针对真实消费者修订少量草案，逐项实现、双实现/替身替换验证，再组合为 GOVM preset。

不采用整体移植旧 Core：旧 Core 混合存储、执行、网络和共识，会带入演示行为和固定协议假设。也不一次性为全部文档目标建立空插件：无真实消费方的预留契约只标记设计，不能注册一个总返回成功的实现。

## 分层与独立仓库

- core：继续只提供格式无关的对象、执行、存储与生命周期基础；本轮不预设新增必选方法。
- cap：可选能力边界。优先落实现有 signer、body、message、state、contract；必要增量见下文。
- 领域：签名套件、成员策略、PoA 规则、代码存取、Host、费用策略分别维护自身不变量；只在实际有替换需要时拆成插件。
- plugins：New(Config, Deps) 纯构造，依赖是最小能力接口，不导入其他插件调用 New，不按全局注册表查运行时服务。
- 外部 VM 仓库：导入 cap，提供 Runtime、Compiler 与 ABI 工具插件；框架及 plugins/all 不反向 import 具体 VM。
- 最终节点程序：显式 import 所选插件，通过 PluginKit 配置图装配。按需加载先指编译期注册、运行期选择配置；不引入 Go .so 热加载要求。

候选目录名和 Kind 是设计名称，实施时以真实消费者验证后固定。

## 接口一 已有基础保持复用

现有 Transaction/Block/ID、TransactionCodec、Executor、Ledger、BranchPolicy、MessageRouter、Signer/Verifier、Journal 等不重新定义同义接口。不要把 GOVM 的 20 字节地址、本片 ID、三个邻片、gas 或验证者字段加入所有交易/区块。

GOVM 地址属于新的模型/协议族；不能让它无条件实现当前 32 字节资产族 Currency。费用收取与发放通过 GOVM 模型的显式策略组合，避免 composed 现有 Currency 路径强制地址转换。

## 接口二 验证者与签名

现有 signer.Signer/Verifier 的版本化套件、Domain、不可导出语义保留。先实现一个完整 Ed25519 套件及 keystore 签名适配，随后以独立套件插件增加其他算法。签名载荷必须有唯一规范编码，绑定链/创世、用途与版本；地址必须与实际公钥绑定。

现有 cap/validator 草案强制 ProtocolBound 并让 Provider 自己按 parent ID 查询，不适合静态配置插件，会产生 protocol → rule → provider → ledger → protocol 构造环。拟修订为消费方提供已校验父块、提供者返回冻结集合：

```go
type Provider interface {
    Identity() core.ID
    Members(context.Context, core.Block) (Set, error)
}
```

Identity 绑定提供者语义版本、有序成员、成员 ID、公钥及套件。Set 保留 Anchor、ID、Members；Anchor 等于父块 ID，ID 绑定本次有序集合，返回副本。父块存在性、协议与祖先绑定由调用入口验证；静态提供者不读取账本，也不宣称能查询任意未知 anchor。

第一版仅实现冻结的创世成员集。动态集合不能通过本机 UpdateValidators 改动共识输入；未来需由统一验证入口显式传入父状态/祖先 View，生产、导入、侧链与恢复使用相同输入。当前 composed.Rule 没有父状态参数，不通过反射或类型断言偷取具体 Ledger；增加动态消费者时再调整封装族验证上下文。

PoA 的轮值策略只在有跨协议族替换需求时提升为 cap；首批可以留在 PoA 领域契约。成员选择、证明验算、封印签名、生产调度和选链分别组合。composed 与 GOVM 若使用不同头格式，各有薄适配，共享规则/套件能力，不强行共用 Header。

空交易块正常签名；缺席按创世绑定的 slot 规则允许后续授权 slot 推进，不制造无签名替代块。slot、时间单位与轮值参数显式版本化。第一版 Choice 不返回未经证书支持的最终确认点；不宣称单签 PoA 具备 BFT 最终性。

## 接口三 引用块正文与归档

复用 body.References、TransactionSource 和 Materializer，增加独立持久正文能力：

```go
type TransactionArchive interface {
    body.TransactionSource
    Put(context.Context, core.Transaction) error
}
```

Put 冻结并验证规范编码/ID，幂等保存相同内容；相同 ID 对应不同规范字节必须拒绝。返回错误不代表可以删除已有内容。归档不提供 mempool Remove 或 canonical detach 删除入口。所有已保存分支的交易都须可重放，不只保留主链索引。

归档可复用 cap/storage，但使用独立拥有的句柄及协议身份绑定。依赖图为 protocol → archive → local materializer → ledger，无 protocol↔ledger 构造环。

完整路径：

1. 远端 materializer 在 importer 前取齐并校验 ID、顺序、承诺；不能把缺正文当作空交易。
2. 候选验证/执行成功、实际 Commit/Import 前，归档先持久化所引用正文，再原子提交账本。两者独立事务时允许留下未被区块引用的正文，不允许已有可见区块却缺正文；归档故障不推进链头。归档有显式容量限制；admission 默认仍是有界 mempool，或使用有配额 staging，不把所有 accepted 交易永久归档。GC 为独立后续策略，不能删除已保存主链或侧链所需正文。
3. 存储内所有需要完整区块的冻结、读回、分支规划、历史策略视图及恢复路径，统一使用本地 decode+materialize 适配，不暗中联网。不能只修改 Ledger.Block：helpers.prepareCommit、branch 分支规划和 policy 历史视图也有直接 decode，须一并接入；纯 codec 的规范 roundtrip 检查仍无 IO，协议回调保持在锁/数据库事务外。
4. hydration 前后 Block.ID、规范线上编码不变，完整交易是辅助执行数据；Transactions 对未补齐对象明确失败。
5. builder/commit/importer 通过明确适配完成正文可用性保障，admission 是否配置 staging 独立决定；不能仅给 importer 加一个 decorator 就宣布支持引用块。

资源顺序也须显式：归档资源先于 Ledger.Start，后于 Ledger.Stop。拟让根资源列表在显式包含 Ledger 时尊重声明顺序，如 resources: [archive, ledger, network, router, body-server, api]；未显式包含时保留原默认账本先启动行为。全部资源启动成功后才运行生产/同步 workers；停机先取消并等待 workers，再逆序注销正文服务与 Router、关闭网络、账本和归档。服务 Start 部分注册失败必须回滚已注册项；Router 即使无法解绑 Network handler，Stop 后也必须拒绝 dispatch 并等待在途请求。不得在 DecodeBlock 或第一次查询时懒开数据库掩盖生命周期依赖。

## 接口四 消息复用

实现已有 MessageRouter，由它独占 Network.Handle。sync/chain、正文请求服务和未来投票服务分别注册版本化路由，不获取整个 Node/Core。注册冲突原子失败，注销/停机撤销并等待处理中的请求；字节数、并发与时间都有预算。

对端认证只沿用网络已有能力，Router 不凭空产生发送者身份。未来投票若需签名或身份，使用已验证的消息证明/专属认证能力，不把网络地址等同验证者资格。

## 接口五 状态 Journal 与外部 Runtime

先实现现有 Journal 的写/删 overlay、LIFO checkpoint、Release/Revert、所有权与错误约束。为 journal 工厂及代码解析增加最窄读取能力：

```go
type Reader interface {
    Get(context.Context, string) ([]byte, bool, error)
}
type Journal interface {
    Reader
    // 其他方法沿用现有 cap/state.Journal。
}
type Factory interface {
    New(context.Context, Reader) (Journal, error)
}
```

Factory 不拥有或关闭调用方基线；基线在执行期间稳定。Journal 不包含 durable Commit，也不为了适配而强加 Entries/Root。所有变化最终回到同一模型变更集和账本事务。core.StateReader 使用 []byte key 和 error 表示缺失，需要窄适配；只将纯 ErrNotFound 转为 absent，其他及混合故障原样保留，不能用一次 errors.Is 吞掉存储错误。

现有 Runtime 只接代码 ID，CodeProvider 又没有状态视图，会诱导实现读取可变全局代码表。拟改为显式解析并传入已验证 Artifact：

```go
type CodeProvider interface {
    Code(context.Context, state.Reader, core.ID) (Artifact, error)
}
type Runtime interface {
    Identity() RuntimeID
    Execute(context.Context, Artifact, Request, Host) (Result, error)
}
```

Request.Code 与 Artifact.ID 必须匹配；内容地址覆盖版本化 RuntimeID 和代码字节。代码提供者读取当前调用的 journal 视图，支持同区块部署后调用和分叉部署。Runtime 不持有 Ledger、可变全局代码表或本地路径注册表。

框架实现以下职责：

- 代码服务：代码与部署记录在链状态中；地址由具体部署策略决定，不使用本机时间。
- Host 工厂：限制本合约状态命名空间，提供确定性执行上下文、受限事件缓冲，不暴露数据库句柄。
- 调用编排：认证 caller、查代码、选择注入的 Runtime、处理 Result 和回执。
- 状态与事件一起回滚；Runtime 使用 Host.State 的 checkpoint 时，Host 包装层也必须同步 checkpoint/revert 事件，不能只回滚 KV。
- 合约 Revert/OutOfGas 与本地故障分开；Used 不超过 Budget，取消或本地超时不能伪装成共识 OutOfGas。

外部 VM 仓库实现真正的 Runtime；Compiler、语言安全检查、ABI 提取/编解码等为独立工具能力，不能在共识执行路径临时编译源码。RuntimeID 绑定代码格式、指令语义、计量表和 Host ABI 版本，允许集合进入链配置/创世绑定。缺 Runtime 时明确不支持合约交易；仅在 _test.go 使用替身，不注册生产假 VM。

外部插件的构造参数使用 cap 类型，在最终节点入口注册，例如：

```go
// 最终节点程序，不是 GoChainKit/plugins/all。
import _ "example.org/independent-vm/plugins/runtime"
```

上述路径只是外部模块入口示意，不表示仓库已存在。跨合约调用、Objects、声明读写集和并行调度等待实际 VM/模型消费者；若以后并行执行，实际读写必须核对声明，结果等价于规定串行顺序。

## 接口六 费用与 GOVM 模型

GOVM 的 GasFeeCap 在旧实现中既与单价比较又截断总费用，不能原样继承。新策略明确把总费用上限与单价区分，固定价格版本先要求最大费用可被预留，检查所有乘加溢出。

费用策略的最小草图如下，仅由需要 gas 的模型消费：

```go
type Budget struct { Limit, UnitPrice, MaxTotalFee uint64 }
type Charge struct { Used, Amount, Refund uint64 }
type Policy interface {
    Identity() string
    Reserve(Budget) (uint64, error)
    Settle(Budget, uint64) (Charge, error)
}
```

固定价格版本要求 Reserve = Limit × UnitPrice ≤ MaxTotalFee；乘法溢出或上限不足直接拒绝。Settle 要求 Used ≤ Limit，Amount = Used × UnitPrice，Refund = Reserve − Amount。intrinsic > Limit 拒绝；传给 Runtime 的 Budget = Limit − intrinsic，总 Used = intrinsic + Runtime.Used，避免重复计费或漏算。

intrinsic gas 计算可留在 GOVM 交易族内部策略，运行指令计量属于 Runtime；是否消费 nonce、如何分配费用是模型规则。有效调用进入执行后，费用/nonce 在外层 checkpoint；合约状态与事件在内层 checkpoint。确定性 Revert 保留规则要求的外层变化，本地故障放弃整个候选执行。不存在无条件实现 Currency 或把余额/gas 塞入基础 Engine 的要求。

## 后续扩展接口的准入

- 高度查询：实现已有 query.HeightIndex，查询显式 anchor，不新增全局高度字段。
- 钱包：先独立本地服务，复用 Store、Signer 和协议签名适配。公钥元数据/列表与私钥分开；导出为单独可选能力。具体接口随首个钱包消费者确定，不把大 WalletAPI 塞进通用 HTTP。
- 管理：PeerControl、ProductionControl、观测各自独立；默认不加入公共查询服务器。未实现不能返回成功；权限和管理监听入口在实现该批功能时明确配置。
- 分片：当前先保存单分片和显式外链引用。邻接策略、外链头来源、证明验证、跨片消息防重放/结算、子片创世是不同职责，待具体协议规则确定后再发布最小 cap。不能把固定三个邻片或 ShardManager 大接口写进 core。
- LevelDB、其他签名套件等独立能力：通过既有能力接口追加实现，不改变消费方算法分支。独立仓库采用相同外部插件装配方式。

## 插件职责与 Deps 草图

| 候选插件 | 单一职责 | 主要 Deps |
| --- | --- | --- |
| signer/ed25519 | 域分离签名 | 密钥材料提供者或明确 keystore 适配 |
| verifier/ed25519 | 独立验签 | 无私钥依赖 |
| validators/static | 冻结创世成员集 | 无账本依赖 |
| rules/poa | 轮值与证明规则 | Members、Verifier、轮值策略 |
| consensus/authority | 出块调度与封印协调 | Ledger 只读视图、Builder、Signer/Sealer、Importer、Replication |
| choice/authority | 分叉排序 | 已绑定的分数/比较策略，无私钥 |
| message/router | 网络消息复用 | Network |
| archive/transactions | 持久正文可用性 | Backend、协议交易 codec/validator |
| body/local | 本地补齐 | References、Archive |
| body/fetch | 远端受限补齐 | References、Archive、远端 Source |
| body/server | 正文请求服务 | Router、Archive、Codec |
| state/journal | 执行 overlay 与回滚 | 基线通过每次调用传入 |
| contract/code | 内容寻址代码存取 | 当前调用 Reader/Journal |
| contract/host | 隔离上下文及事件 | Journal 工厂、执行上下文 |
| contract/processor | 部署/调用编排 | Code、HostFactory、Runtime 列表、部署策略 |
| fees/fixed | 预留/结算计算 | 无数据库依赖 |
| model/govm | 账户、转账、gas 与业务操作 | 费用、合约处理、协议身份适配 |
| protocol/govm | 格式、ID、承诺与规则绑定 | Model、PoA 验证适配、Choice、引用策略 |

表中列出职责而非强制一行一个目录。必须共同维护的原子不变量保留在同一所有者；仅转发一个私有函数且没有替换需求的包装不单独注册插件。

## 推进顺序与验收

1. **基础能力**：确定性 Journal 与事件回滚基础、签名套件/独立验签、静态成员集；用替换实现或测试替身验证依赖边界。
2. **签名 PoA**：先在已有完整正文协议上验证 PoA × account/UTXO；真实签名、错误轮值/跨链重放/伪造空块拒绝；离线 slot 行为与分叉规则明确，重连与恢复收敛。
3. **正文与消息**：Router 接替 sync 的单 handler；归档/远端补齐/本地恢复贯通。清空池且断网重启仍可重放全部分支；缺失或损坏正文明确启动失败；归档失败不能入链。
4. **GOVM 单分片闭环**：独立格式、20 字节地址、签名转账、nonce/gas、引用体、锚定查询。三节点运行，重组与重开状态一致。
5. **合约框架**：代码状态、Host 与 processor、费用/回滚边界、外部 Runtime 契约测试。没有真实外部 Runtime 时仅交付框架基础和拒绝路径，不宣称合约执行可用。
6. **外部 VM 接入**：独立仓库真正实现并通过兼容性测试；最终节点 import/配置后验证部署、调用、Revert、OutOfGas、重放和跨 Runtime 替换。
7. **钱包/管理/观测**：独立本地服务及明确管理入口，绝不以旧 no-op 充当功能完成。
8. **多分片与对象并行**：先确定可验证的外链头、证明和结算语义，再添加真实 cap/插件；分片字段存在或文档描述不算实现。

每批先写失败用例、实现最小插件、规格审查和独立质量审查，再做相关组合验证。阶段结束更新“契约/实现/外部依赖”状态；只在用户要求时提交或推送。
