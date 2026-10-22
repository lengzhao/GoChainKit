# 历史设计、实施与评估索引

保留原文件路径，按逻辑归档，避免无益迁移及外链失效。读当前能力先看 [status](../status.md)，读契约看 [interfaces](../interfaces.md)。

| 资料 | 状态与使用方式 |
| --- | --- |
| [初始架构](../plans/2026-10-17-gochainkit-design.md)、[初始实施](../plans/2026-10-17-gochainkit-implementation.md) | 历史方向；固定核心格式已被协议接口重构替代 |
| [架构评估](../architecture-review.md)、[评估执行](../plans/2026-10-17-architecture-execution.md) | 历史问题及已落实改进，不能当作当前缺陷清单 |
| [组合抽象评估](../composed-abstraction.md) | 建议 1—5 已采纳，后续设计仍需消费者 |
| [组合目标](../plans/2026-10-17-composable-consensus-ledger-design.md)、[组合实施](../plans/2026-10-17-composable-consensus-ledger-implementation.md) | 部分交付：PoW/模型已有，DPoS 未交付且优先级待确认 |
| [协议接口](../plans/2026-10-17-protocol-interfaces.md)、[cap 分层](../plans/2026-10-17-capability-layers.md) | 迁移记录；当前规范见接口与能力页 |
| [依赖细化](../plans/2026-10-17-plugin-deps-refinement.md)、[实现简化](../plans/2026-10-17-implementation-simplification.md) | 实施/决策历史，当前目录与 API 以源码为准 |
| [PoW 网络设计](../plans/2026-10-17-pow-network-design.md)、[实施](../plans/2026-10-17-pow-network-implementation.md) | 简化 powkv 阶段记录，不等于所有 PoW 规则 |
| [HTTP 实施](../plans/2026-10-17-http-api.md)、[存储/密钥实施](../plans/2026-10-18-storage-keystore.md) | 已有实施记录；使用现行 API/storage/key-management 专题 |
| [GOVM 差距基线](../govm-feature-map.md)、[能力设计](../plans/2026-10-20-govm-cap-design.md) | 外部源码快照与部分实施草案，不是当前实现矩阵 |
| [GOVM 首批实施](../plans/2026-10-21-govm-foundation.md) | Journal、签名、静态成员与 PoA 的完成记录；后续未包含 |

新增未落地方案可在有实际需求时进入 rfcs；已采纳的关键取舍可提炼为 ADR。当前不为目录完整性制造空文件或对未决目标标“取消”。
