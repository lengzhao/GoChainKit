# 文档地图

GoChainKit 面向通过 Go 和 PluginKit 组装新链的开发者。先区分“通用契约”“当前插件实现”和“未来草案”，再选择组件。

| 读者 | 推荐顺序 |
| --- | --- |
| 试用节点 | [概览](overview.md) → [快启](tutorials/quickstart.md) → [第一笔交易](tutorials/first-transaction.md) → [HTTP API](api.md) → [恢复与排错](guides/compatibility-recovery.md) |
| 链/插件开发者 | [能力矩阵](status.md) → [接口](interfaces.md) / [能力分层](capabilities.md) → [自定义链](tutorials/custom-chain.md) → [配置参考](reference/plugins.md) → [扩展验证](contributing/testing.md) |
| 框架贡献者 | [概览](overview.md) → [开发规范](development-conventions.md) → [测试](contributing/testing.md) → 目标能力规范 → [路线图](roadmap.md) / [历史索引](archive/index.md) |

## 当前规范与指南

- 领域与组合：[执行模型](execution-models.md)、[规则与选链组合](composition.md)、[接口契约](interfaces.md)、[能力状态细节](capabilities.md)。
- 运行：[节点生命周期](node-runtime.md)、[网络与简化 PoW](network-consensus.md)、[单节点出块流水线](block-production.md)。
- 存储与访问：[事务/分支存储](storage.md)、[密钥与身份](key-management.md)、[HTTP API](api.md)。
- GOVM 是场景能力建设方向：[已完成基础与 PoA](govm-foundation.md)；[原始功能差距](govm-feature-map.md) 是带基线的历史对齐材料，不是当前实现清单。

## 状态约定

“已实现”须有具体消费方和测试入口；测试文件存在不等于本轮已运行。“草案”是待验证契约；“历史”记录当时状态；“待确认”不是已取消。能力状态以 [status](status.md) 汇总，各规范页提供详细限制。设计日期和近期实现状态不得混为同一时刻。

已有专题暂不大规模移动，避免破坏链接；逻辑上按 tutorials、guides、reference、contributing、路线图、草案/决策及 archive 分类。未实施设计留在原 plans 并由 [历史索引](archive/index.md) 标明状态，不创建空的 RFC/ADR 目录。
