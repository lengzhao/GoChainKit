# 插件依赖与组合契约调整计划

**目标：**落实已批准的接口必要性、高内聚、低耦合和类型化 Deps 规范。

**架构：**领域构造函数不依赖装配框架；插件通过 Config/Deps 入口适配。只公开已有真实消费点的上下文能力，构造期固定经济能力组合。

**技术栈：**Go、PluginKit、现有 composed 协议。

1. 在现有上下文回归中断言具名 HeightContext、TimestampContext、StateCommitmentContext，先确认缺失接口导致编译失败。
2. 在 model 定义这些可选接口，执行上下文保留现有方法及语义。
3. Protocol 构造时解析 Economics 和 Currency 并保存，执行时不重复探测；保留无货币模型及不兼容组合测试。
4. 为 account、UTXO、PoW 叶子插件增加空 Deps 与薄构造适配，保留领域构造函数；通过真实 PluginKit 配置装配验证。
5. 补齐执行顺序、分数编码及奖励归属契约；更新规范当前状态。
6. 运行相关包测试、race、vet、格式和差异检查。不扩大到新共识或模型实现。
