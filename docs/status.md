# 当前能力与组合矩阵

文档核对日期：2026-10-22。实现基线：`bb24a7b30a90ddb569960521a5e535f6d9225d1f`。本表区分本地实现、契约草案与尚未交付目标；测试入口用于定位，实际运行结果应在交付记录中单列。

| 能力 | 当前状态 / 消费方 | 运行或验证入口 | 边界 |
| --- | --- | --- | --- |
| 格式无关 core | 已实现，协议/执行/账本消费 | `core/contracts.go`、`tests/integration/production_test.go` | 接口通用不等于现有存储支持任意拓扑 |
| KV 单节点 | 已实现 | `presets/single.yaml` | 无资产余额/费用；开发闭环 |
| 简化 PoW | 已实现，powkv + PoW runtime | `presets/pow.yaml` | 固定 8 位目标，无奖励/调难 |
| 规则级 PoW | 已实现，composed + account/UTXO | `presets/pow-account.yaml`、`presets/pow-utxo.yaml` | 有调难、奖励与重组；非 Bitcoin 兼容 |
| 静态签名 PoA | 已实现，Rule/Choice/Sealer/authority runtime | [初始化与运行](govm-foundation.md)、`tests/integration/authority_network_test.go` | 无 BFT 最终性/动态成员；观察者无需共识私钥 |
| 账户 / UTXO | 已实现，可与 PoW/PoA 组合 | `protocol/composed/authority_test.go`、`composition_test.go` | 具体资产族，不强制所有模型采用 |
| libp2p / 导入 / 同步 | 已实现，`importer/branch`、`sync/chain` | `tests/integration/pow_network_test.go` | 单父分支，sync 独占 Handler |
| 内存 / Bolt | 已实现，账本/后端分离 | `plugins/storage`、`storage/backend` | 全量快照/分支图，未优化长期裁剪 |
| HTTP / 模型查询 | 已实现，领域服务 + 可选路由 | `tests/integration/api_test.go` | 无钱包签名/管理接口；受理不保证确认 |
| 密钥 / 身份 / 签名 | 已实现，加密 Store、P2P 适配、Ed25519 | `keystore`、`identity/libp2p`、`signer` | 无 HD 钱包/自动轮换，Signer 不强制导出私钥 |
| Journal | 已实现，执行局部 KV 与 LIFO checkpoint | `state/journal` | 无 durable Commit、Host 或事件一体回滚 |
| 引用体 / MessageRouter | 草案，尚未贯通现有运行链路 | `cap/body`、`cap/message` | 不代表正文归档/断网恢复可用 |
| 合约 Runtime / Host | 草案，无真实 VM/完整调用 | `cap/contract` | 拟议签名与当前接口有差异，见路线图 |
| DPoS / 动态成员 | 原设计目标，尚未交付 | [原组合目标](plans/2026-10-17-composable-consensus-ledger-design.md) | PoA 不可替代选举/投票/防双签验收 |
| GOVM 专属协议/gas、分片 | 后续场景设计 | [GOVM 草案](plans/2026-10-20-govm-cap-design.md) | 不代表已有运行实现 |

## 选择路径

先用 single 验证执行闭环；需要分叉网络时选 powkv，或选择有资产模型/经济规则的 composed PoW。需要冻结成员签名时看 PoA 场景指南。选定协议、创世与规则后再配置本地端口/路径，不把协议变更当作普通节点参数修改。

完整能力接口消费者表保留在 [capabilities](capabilities.md)，不在此复制接口定义。涉及安全、性能和外部 VM 的声明，必须以具体范围与验证结果为准。
