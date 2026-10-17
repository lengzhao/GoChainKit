# Cap 扩展能力分层实施计划

**Goal:** 保持 core 通用契约不变，在 cap 按能力定义可演进的扩展接口，避免领域契约散落在实现包。

**Architecture:** core 不依赖 cap、model、protocol 或 plugins；cap 可以依赖 core 和其他明确的能力包，但不依赖具体实现、装配框架或存储网络库。model/protocol/rules/plugins 消费这些能力，插件通过类型化 Deps 组合。用户明确不保留兼容性：所有引用直接切换新路径，删除旧接口和过渡适配，不重复维护两套接口。

**Tech Stack:** Go 1.26.1、PluginKit 现有装配及测试。

## 范围和状态

本轮实现 cap 契约和已有执行能力的实际接入；不把声明接口等同于实现 PoA、合约 VM 或多分片。PoA、哈希交易体、合约/Journal、查询和跨链实现仍按前述顺序单独验收。cap 接口允许随真实消费需求调整；核心接口变化需单独说明不可通过可选能力解决的原因。

1. 首先验证新的 cap/model 导入尚不存在，再将现有 Engine、Transition、ContextualEngine 及执行上下文能力迁入 cap/model。删除旧 model 接口与 Apply 包装，生产及测试调用方直接切换 cap/model 和 core.ApplyStateChanges，不留兼容别名。
2. 新增已确认需求的扩展契约：validator（父块绑定集合）、signer（独立密钥与签名）、body（引用解析与交易体装配）、state（隔离工作区与局部回滚）、contract（Runtime/代码/编译）、message（单网络handler之上的路由）、query（提交及可选按高度查询）。接口写明消费方、确定性、对象所有权、错误和生命周期，不加入空实现，不强制资产/账户字段，不定义万能容器。
3. 多分片拓扑、动态集合激活、轻客户端证明和并行调度只在文档记录扩展方向，不预建没有实际契约需求的模块或接口。
4. 编译所有新包；以实际 composed 执行验证迁移。通过依赖分析确保 core 无反向依赖，cap 无实现层/PluginKit依赖；独立规格与质量审查。
5. 运行适当测试、race、vet、格式和差异检查。已有配置保持运行；如扩展契约尚无运行实现，文档明确标注，不以测试替身冒充实际功能。


## 追加：既有扩展能力归位

用户明确无需兼容之后，继续将既有单父分支/选链能力移至 cap/chain，PoW/封印/提案验证能力移至 cap/consensus，网络移至 cap/network，同步与导入移至 cap/replication。更新所有消费方，不在 core 留旧定义或别名。将既有可选 Currency/Escrow 与其数据契约归位 cap/asset，AddressOf/Fee/Fees/Add 等实现助手留在 model/asset。清除从未使用的旧 Message/Transport 双重网络抽象。通用 Protocol、Block、Transaction、状态与原子提交、Node 等仍属于 core；具体 composed.Rule/Choice 依赖格式族，不机械搬入 cap。

此步骤只改变 Go 类型声明的归属和导入路径，不改变线格式、数据格式或共识行为。第一批 cap 模型迁移及新增契约通过规格、质量审查后再顺序执行，避免并行修改同一消费方。


## 审查记录

第一批规格与质量审查通过。Checkpoint 使用实现定义的强类型 token，由 Journal 检查实例归属、LIFO 和失效状态；不加入令牌生成算法。签名 Algorithm 表示完整版本化套件，必须固定参数、规范消息/域编码和预哈希，同套件独立实现互操作，避免插件各自选择编码。


## 验证结果

两批规格及质量审查均通过；cap/model 是唯一模型契约定义，既有可选分支、共识、网络、复制、资产契约完成归位，无旧接口兼容层。15 个 core/cap 包依赖检查通过：core 无反向依赖，cap 无实现层或第三方后端依赖。全仓 `go test -race ./...`、`go vet ./...`、格式与差异检查通过。四份 CLI 配置均可重开迁移前创建的临时测试账本。

本轮新增 validator/signer/body/state/contract/message/query 仍为扩展契约草案，尚未提供运行实现；后续按消费者落地后再调整契约。不得将此轮描述为已实现 PoA、VM、引用体同步或多分片。
