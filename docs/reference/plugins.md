# 插件与配置参考

本页是内置插件导航，不复制全部构造类型。完整字段与校验以所链接 Go 源码为准，示例以 [presets](../../presets/) 为准。尚未实现的 Router、Runtime、归档不能写进可用 Kind 列表。

## 三种名称

| 名称 | 示例 | 作用 |
| --- | --- | --- |
| Go 包路径 | `plugins/node` | 编译期导入注册适配 |
| Kind / use | `node/default` | 选择注册构造函数 |
| 配置实例 ID | `node` | 引用依赖图中的一个对象 |

根配置只有 `root` 与 `plugins`；root 是实例 ID。Config 是可序列化参数，Deps 是类型化能力引用；不是另建服务定位器。节点程序须导入所用插件包，配置中写 use 不会下载/加载任意 Go 插件。

## 能力目录

| Kind | 主要 Config / Deps | 资源与源码入口 |
| --- | --- | --- |
| protocol/kv、protocol/powkv | chainID；powkv 详见 preset | 纯协议，[适配](../../plugins/protocol/) |
| protocol/composed、protocol/composed-pow | network/genesisTime/producer；model/rule/choice | [构造与能力要求](../../plugins/protocol/composed.go) |
| model/account、model/utxo | 模型网络/创世分配，字段按 Config | [注册](../../plugins/model/models.go)、[模型](../execution-models.md) |
| rules/pow | 调难/奖励参数；完整规则绑定身份 | [适配](../../plugins/rules/pow.go)、[规则 Config](../../rules/pow/rules.go) |
| rules/poa、choice/authority、seal/authority | 静态成员/验签/签名，依赖按职责分开 | [适配](../../plugins/authority/authority.go)、[PoA](../govm-foundation.md) |
| executor/kv、executor/composed、executor/powkv | protocol；composed 另有 history，powkv 注入 inner 执行器 | [通用适配](../../plugins/executor/)、[powkv 适配](../../plugins/protocol/powkv.go) |
| backend/bolt、backend/memory | bolt.path；不依赖链规则 | [适配](../../plugins/backend/backend.go)，Ledger 拥有打开的句柄 |
| storage/kv、storage/memory | protocol/backend/可选 policy 按具体实现 | [存储](../storage.md)、[适配](../../plugins/storage/) |
| mempool/memory | maxTransactions/maxBytes；protocol | [Config](../../plugins/mempool/memory.go)，有界易失池 |
| selector/fifo、builder/basic | builder.maxTransactionBytes；protocol/ledger/pool/executor/selector | [构块器](../../plugins/builder/basic.go) |
| node/coordinator、node/default | 根 shutdownTimeout；ledger/host/consensus/resources/workers | [适配](../../plugins/node/)、[运行契约](../node-runtime.md) |
| consensus/single | interval；无状态协议依赖 | [实现](../../plugins/consensus/single.go)，由 Node 驱动 Run |
| consensus/pow、consensus/authority | 运行调度；builder/importer/replication 等 | [实现](../../plugins/consensus/)，生产与同步共同监督 |
| importer/branch、sync/chain | sync.interval；protocol/ledger/executor或network/pool/importer | [实现](../../plugins/replication/)，sync 独占 Handler |
| network/libp2p | listen/seeds/maxMessageBytes/timeout；protocol/identity | [Config](../../plugins/network/libp2p.go)，根资源管理 |
| secret/env、keystore/encrypted-file、keystore/memory、identity/libp2p | environment/directory/keyID，secret/store | [密钥](../key-management.md)，不在 YAML 保存口令 |
| signer/ed25519-keystore、signer/ed25519-verifier、validator/static | keyID/store 或冻结成员/公钥 | [基础能力](../govm-foundation.md) |
| state/journal | 条目/字节/检查点预算 | [实现](../../plugins/state/journal/journal.go)，无持久提交 |
| api/query、api/http、api/account、api/utxo | query/模块/监听及预算 | [API 契约、默认值与 YAML](../api.md) |

## 最小装配与默认值

从 [single](../../presets/single.yaml) 开始；[pow](../../presets/pow.yaml) 是简化 powkv，[pow-account](../../presets/pow-account.yaml)/[pow-utxo](../../presets/pow-utxo.yaml) 是规则级组合。PoA 由 [初始化器](../govm-foundation.md) 生成带随机密钥的配置，没有可复制的公开共识私钥。

single 实例配置：池 1000 笔/16 MiB、builder 1.5 MiB、出块间隔 1s、根关闭 5s、HTTP 8080。它们是 preset 的显式值，不代表所有构造器默认值。API 构造默认值见 API 页；网络默认 4 MiB/5s、并行限制见网络页；Journal 默认预算见基础能力页。其他字段直接读取具体 Config 与构造校验，避免重复维护漂移的全量表。

资源归属：Node 管 Ledger 和 resources 的启动/逆序关闭；运行任务放 workers/consensus。API 同时列 resources 与 workers。Backend 打开器不是已打开数据库；不应额外列为根资源。多个组件必须引用同一 Ledger owner。

## 常见错误

- 未导入注册包 → unknown Kind；仅添加 YAML 不能补注册。
- Deps 类型不兼容/缺失或循环 → 构图失败，不应在运行期偷偷替换默认依赖。
- 相同协议却用了两个 Ledger → owner 校验拒绝；不要靠相同路径/描述符推断同实例。
- 修改规则/创世后复用旧库 → 绑定校验失败，见 [兼容恢复](../guides/compatibility-recovery.md)。
- API 没进 workers、资源重复关闭、sync Handler 被其他插件覆盖 → 生命周期/能力组合错误，按现成 preset 逐项核对。
