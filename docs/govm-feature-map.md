# GOVM 功能对齐与源码依据

2026 年 10 月 20 日。目标是以单一职责插件及类型化 Deps 实现 GOVM 的有效能力，并为独立 VM 仓库提供稳定接入边界。延续项目约定，不要求旧网络、磁盘或交易字节兼容。下表记录分析基线时的差距，不表示相应插件已经实现；后续实施状态见 [基础能力说明](govm-foundation.md) 及 [实施记录](plans/2026-10-21-govm-foundation.md)。

## 分析基线

- GoChainKit：`6ccdcc2`。
- [GOVM](https://github.com/lengzhao/govm/tree/558f9b51c88e5a4ecaedcffed18ddead1158d671)：`558f9b51c88e5a4ecaedcffed18ddead1158d671`。
- [VM](https://github.com/lengzhao/vm/tree/b91515c8aef8b42350a09c79edf7f111a3238c2e)：`b91515c8aef8b42350a09c79edf7f111a3238c2e`。

GOVM 与 VM 的 main 引用已通过远端查询核对，以上快照与查询结果一致。以下结论来自静态源码和测试内容检查，未把原仓库文档或测试文件的存在当作运行验证。

## 功能对齐

| 功能 | GOVM 源码状态 | GoChainKit 现状与所需工作 |
| --- | --- | --- |
| 区块、交易、地址 | 有独立格式：20 字节地址、分片 ID、gas 字段、交易哈希列表 | core 已允许任意格式；新增 GOVM 协议族与模型适配，不把 20 字节改成所有模型的规则 |
| 签名及地址派生 | Ed25519、ECDSA、secp256k1、Schnorr 有算法实现与测试 | cap/signer 有契约无实现；先提供完整版本化 Ed25519 套件，其他套件独立插件追加；地址派生属于协议族 |
| 验证者与 PoA | 有按高度轮值、配置更新；验签和生成签名含演示逻辑 | 落实真实成员公钥、持久 Signer、可独立替换的轮值规则与生产调度；选链单独实现 |
| 空区块与缺席 | 空交易且无签名可走免验路径 | 空交易块仍正常签名；缺席由明确 slot 规则处理，不制造无授权块 |
| 最终性 | 文档称即时确认，未实现投票证书或抗双签协议 | 开发级 PoA 不宣称 BFT 最终性；只有实际证书协议实现后才提供相应 Finalized 策略 |
| 转账与 nonce | 有余额读写、nonce 更新，但状态更新为分次写入 | 沿用框架隔离执行与原子提交，验证地址/公钥绑定、序号、金额及费用溢出，错误不能伪装成零余额 |
| Gas | 基础量加数据长度，再按 limit/cap 截断，无 VM 计费 | 提供明确版本化 intrinsic gas、费用预留和结算策略；与 VM 指令计量分开 |
| 引用式区块体 | 区块保存交易哈希，块内交易验证仍 TODO | 现有 cap/body 草案可复用；必须实现持久正文归档、受限补齐和本地恢复接线 |
| 交易池 | 有磁盘交易记录，重启未重载 pending 选择集；移除会删记录 | 当前框架有有界内存池；正文归档不能等同 mempool。若恢复 pending，再单独实现有界持久池 |
| 存储与恢复 | 有 LevelDB/命名空间，但更新与恢复不等同完整原子链状态 | 现有 cap/storage 与通用账本保留，使用新后端接口，不复制分次提交路径；LevelDB 仅在需要替换数据库时补插件 |
| 网络同步 | 有高度查询和区块传输，包含广播、sleep 与演示路径 | 保留已验证的分支同步/导入；实现 MessageRouter，使同步、正文请求和未来投票分别拥有路由 |
| 分片 | 有本片 ID 和三个邻片哈希字段；非零邻片引用验证未实现 | 第一阶段只实现单分片及显式引用格式；跨片头验证、证明、结算与子片创世分别设计，不能靠字段存在宣称已支持 |
| 链查询 API | 有区块/交易接口；余额、账户等存在固定返回值 | 现有框架真实快照查询已可复用；补锚定高度查询与 GOVM 模型路由，不复制默认余额 |
| 钱包 | 有创建、导入、导出、签名、列表 | 已有加密 Store；新增独立本地钱包/密钥元数据与交易签名适配；导出是可选能力，不是 Signer 的必选方法 |
| 节点管理 | Connect 有调用；RemovePeer、Start/StopMining 返回成功但无实现 | 分开 PeerControl、生产控制和观测能力；未实现必须明确不支持。管理入口与公共查询入口隔离 |
| VM 编译/部署/执行 | VM engine 当前编译 shell 占位文件、用时间生成地址、返回格式化执行字符串 | 框架不能直接包装成真实 Runtime。独立 VM 仓库提供 Runtime/Compiler/工具；框架提供 journal、代码状态、Host 与调用编排 |
| ABI、Objects 与并行 | ABI 有 AST 元信息提取；Objects、读写集、并行属于文档设计 | ABI 工具留 VM/工具仓库；对象和调度接口等实际消费者出现后再加，首批以确定性串行执行为验收基准 |

## 关键源码定位

1. 协议形态：[types/types.go:6](https://github.com/lengzhao/govm/blob/558f9b51c88e5a4ecaedcffed18ddead1158d671/types/types.go#L6)、区块与分片字段第 10—24 行、交易及 gas 第 34—50 行、账户代码字段第 74—80 行。
2. PoA 验签使用临时生成公钥：[consensus/consensus.go:191](https://github.com/lengzhao/govm/blob/558f9b51c88e5a4ecaedcffed18ddead1158d671/consensus/consensus.go#L191)；生成器演示签名：[generator/generator.go:180](https://github.com/lengzhao/govm/blob/558f9b51c88e5a4ecaedcffed18ddead1158d671/generator/generator.go#L180)。生成状态根与邻片引用为空见第 144—145 行。
3. 块内交易验证 TODO：[core/blockchain.go:218](https://github.com/lengzhao/govm/blob/558f9b51c88e5a4ecaedcffed18ddead1158d671/core/blockchain.go#L218)。交易逐项改余额后才更新 nonce：[core/transaction.go:141](https://github.com/lengzhao/govm/blob/558f9b51c88e5a4ecaedcffed18ddead1158d671/core/transaction.go#L141)。Gas 计算见第 342—365 行。
4. API 默认余额：[api/api_impl.go:509](https://github.com/lengzhao/govm/blob/558f9b51c88e5a4ecaedcffed18ddead1158d671/api/api_impl.go#L509)；钱包签名使用 JSON 见第 656—674 行，交易验证使用 binary 编码见 core/transaction.go 第 78 行起，需统一规范签名载荷。未实现管理调用见 api_impl.go 第 706—724 行。
5. VM 占位实现：[engine.go:63](https://github.com/lengzhao/vm/blob/b91515c8aef8b42350a09c79edf7f111a3238c2e/engine.go#L63)、第 99—145 行。ABI AST 提取：[abi/abi.go:45](https://github.com/lengzhao/vm/blob/b91515c8aef8b42350a09c79edf7f111a3238c2e/abi/abi.go#L45)。对象/并行设计：[execution_environment.md](https://github.com/lengzhao/vm/blob/b91515c8aef8b42350a09c79edf7f111a3238c2e/docs/execution_environment.md)。

## 落地原则

复用已有 core 与 cap，不按旧目录机械建立对应接口。每项能力必须有真实消费方、可替换语义、确定性/失败/生命周期约束。链格式和业务策略可以留在领域协议族，独立 VM 等模块通过外部 Go module 提供插件。细化接口与推进顺序见 [GOVM 能力设计草案](plans/2026-10-20-govm-cap-design.md)。
