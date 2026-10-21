# GOVM 基础能力插件

本页记录首批实现的使用约束。GOVM 全量功能范围及后续阶段见 [功能盘点](govm-feature-map.md) 和 [组合设计](plans/2026-10-20-govm-cap-design.md)。VM 继续由独立 Go module 提供，本仓库没有内置 VM 执行引擎。

## 执行局部状态

`state/journal` 插件提供 `cap/state.Factory`。每次执行调用 `Factory.New(ctx, baseline)` 创建独立 Journal，baseline 只需实现 `Reader.Get`。已有 `core.StateReader` 可用 `state/journal.FromStateReader` 窄适配；原快照仍由调用者关闭，且必须在 Journal 生命周期内保持稳定。

Journal 的 `Set`、`Delete` 只修改覆盖层。`Changes` 返回相对 baseline 的写入与删除副本，由执行编排交给现有原子账本提交路径；它不自行持久化。不存在的键与存在的 nil/空值通过 `Get` 的布尔值区分。

检查点采用 LIFO：`Release` 保留当前修改并释放最内层检查点，外层 `Revert` 仍可撤销这些修改。无效、失效、外来 token 或取消操作不会消耗检查点。token 只用于本次执行，不能编码进交易、状态或共识决策。

默认预算为 65,536 个条目、64 MiB 键值字节和 64 层检查点；条目与字节预算包含覆盖层及全部尚未释放的检查点快照。达到预算返回 `journal.ErrLimit`，不留下部分写入。若将预算用于共识执行，配置必须在所有节点上一致并由相应执行模型绑定。

工厂可并发创建多个 Journal，单个 Journal 限一次执行内串行使用。目前只隔离 KV 状态；事件缓冲与嵌套调用 Host 尚未接入，不能把 KV 回滚等同于整个外部调用已经回滚。

## 签名与成员

三个插件分别提供独立能力：

| 插件 | 能力 | Deps |
| --- | --- | --- |
| `signer/ed25519-keystore` | `cap/signer.Signer` | `store: cap/keystore.Store` |
| `signer/ed25519-verifier` | `Verifier`、`KeyValidator` | 无 |
| `validator/static` | `cap/validator.Provider` | 无 |

Signer 通过 `keyID` 指定已经存在的密钥，不生成密钥、不在构造时访问 Store。材料为标准 Ed25519 的 64 字节私钥，读取后校验 seed 与公钥半段一致，并尽力清理临时材料。Store 返回值归调用者所有是这一适配器的前提。验签插件完全不访问 Store。

套件名称固定为 `ed25519/gck-domain-v1`：raw Ed25519、无预哈希，签名帧为字面前缀 `GoChainKit/sign/ed25519/v1\x00`，后接 Chain、Purpose、Version、payload 四个字段，每个字段均先写 uint64 大端字节长度，再写原始字节。前三个字段必须非空，payload 可以为空。Chain 必须标识含创世的精确链实例；相同公钥、不同链或用途不能互相重放。

静态成员配置冻结有序的 `{ID, Key{Algorithm, Bytes}}` 列表；集合身份承诺版本、顺序及全部成员内容。`Members(ctx,parent)` 的 parent 必须由调用方提前校验，返回 `Anchor=parent.ID()` 的独立副本。该插件不查 Ledger、不判断父块存在性，不提供本机动态更新入口。它只校验成员结构和唯一性，套件支持性由使用成员集的规则通过验签能力检查。

## 签名 PoA 的装配边界

`rules/poa` 消费成员与验签能力；`choice/authority` 决定分支排序；`seal/authority` 消费轮值查询与 Signer；`consensus/authority` 组合构块、封印、导入和同步。验块与同步节点不需要持有共识私钥。具体编码规则留在 composed 封装族，不增加 core 的必选字段或方法。

生产调度中的本机时间只用于判断当前是否可签，不参与历史验块。封印获得真实父块与执行定稿后的区块，不能只伪造一个含父块 ID 的对象传给成员插件。导入器执行前调用可选的 composed 候选规则检查，执行后仍完整校验状态承诺和签名；两次检查不能相互替代。

第一版只接冻结成员，slot/epoch 参数和成员提供者身份绑定规则身份，再绑定精确创世。Genesis 构造期间的初始集合检查使用正在构造的创世头，是静态提供者的初始化特例；不据此宣称支持任意动态历史成员插件。动态集合需要后续显式父状态/祖先输入设计。

slot 从 `(timestamp-genesisTime)/slotSeconds` 计算；slot 0 仅用于创世，生产者为有序成员的 `(slot-1) % 成员数`，epoch 为 `slot/epochSlots`。后继区块必须进入更大的 slot，允许跳过缺席轮次。参数时间单位为秒，规则校验不依赖节点本机时钟。选链先比较高度，再取字节序更小的区块 ID；无投票证书，`Finalized` 为空。这个开发级 PoA 尚无抗双签、罚没或 BFT 最终性保证。

## 启动单节点 PoA

通过运行环境提供 `GOCHAINKIT_KEYSTORE_PASSWORD`，然后在仓库根目录执行：

```sh
go run ./cmd/gochainkit-authority-init -directory data/authority
go run ./cmd/gochainkit -config data/authority/node.yaml
# 有界运行与重开验证
go run ./cmd/gochainkit -config data/authority/node.yaml -run-for 8s
```

初始化器只接受不存在的最终目录，生成随机共识密钥并加密保存，创建完整配置；再次初始化同一路径会失败。失败后保留已创建内容供检查，不删除用户数据。配置使用账户模型、单成员、2 秒 slot，默认无创世余额和区块奖励；发送资产交易前应明确设置创世分配。网络及 HTTP 使用本机临时端口，实际地址在启动时打印。

`keys` 中 `consensus` 与 `p2p` 是独立记录，不能混用私钥编码。共识密钥由初始化器显式创建，P2P 身份由身份适配器首次使用时独占创建。节点重开复用原 `node.yaml`、数据库、密钥目录和口令，不重新初始化链。

多节点需共享 network、模型创世分配、genesisTime、slotSeconds、epochSlots、完整有序成员表和 choice；分别配置本地 producer、共识 keyID、磁盘路径、监听地址及 seeds。producer 配置是成员 ID **原始字节**的十六进制编码，不必等于公钥。初始化器为方便使用选取公钥的十六进制文本作为成员 ID，因此生成的 producer 是该文本字节再次编码后的值，不应手工替换成 raw 公钥十六进制。每个节点独立运行初始化器产生的是不同链，不能直接互联当作同一成员网络。

只同步节点使用相同链配置，省略 `consensus.deps.producer`，不加载共识 Signer；仍需独立的 P2P 身份。不要把其他验证者的私钥复制给观察节点。

## 验证范围

- Journal：嵌套 Release/外层 Revert、删除及 nil/空值、字节所有权、checkpoint 总预算、取消、错误 token、底层混合故障及真实存储快照借用。
- 签名与成员：独立标准库按规范帧验签、域隔离、未知套件、畸形私钥/签名、临时材料清理、集合配置绑定及真实 PluginKit 多实例装配。
- PoA：无签名空块、头/签名/链绑定篡改、轮值与 epoch、确定性候选重建、坏远端候选在执行前拒绝、本地检查到构块跨 slot 的重试，以及停机时混合/已提交维护故障保留。
- 两种模型：PoA × account/UTXO 的真实转账、分叉撤销、状态恢复、Bolt 重开后继续生产。
- 三节点：真实 libp2p、Bolt、HTTP 与自动调度，受控原子时钟驱动隔离分叉、明确 ID 平局赢家、重连收敛、跳过缺席轮次、无 Signer 观察者重启追赶；核对快照根、区块承诺和各节点状态一致。HTTP 转账确认、回执绑定和余额在重开后仍可查询。
- 独立进程：初始化随机密钥后实际启动 CLI，查到签名块；关闭重开后保留旧块并继续推进链高。

关键组合用例位于 `protocol/composed/authority_test.go` 和 `tests/integration/authority_network_test.go`。三节点测试的轮值时间采用可控时钟，真实进程验证使用系统时钟；这些开发规模回归不替代公网长期稳定性或恶意验证者安全性验证。

本批没有引用式区块正文归档、消息 Router、GOVM 特有地址/gas 模型、Host 事件回滚、动态验证者、钱包/管理接口或真实外部 VM。它们仍按组合设计的后续阶段接入。
