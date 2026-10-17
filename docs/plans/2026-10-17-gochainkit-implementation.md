# GoChainKit Implementation Plan

> 后续协议接口已重构。当前接口和目录以 [模块与协议接口](../interfaces.md) 为准，本文保留初版执行计划历史。

> 执行要求：按 executing-plans 工作流逐项执行；每项先编写行为测试、确认失败，再实现并确认通过。

**Goal:** 使用 PluginKit 装配可提交交易、单节点出块、确定性执行、原子持久化及重启恢复的 Go 区块链。

**Architecture:** 公共协议与领域接口独立于插件实现；Node 编排共识、构块和提交。状态、区块、回执使用同一事务后端，PluginKit 完成显式依赖注入。

**Tech Stack:** Go 1.26.1、PluginKit v0.1.6、标准库 Ed25519/SHA-256/HTTP；事务存储优先评估 bbolt，选定前核查官方 API 和依赖版本。

**当前执行状态：** [编程与模块接口规范](../development-conventions.md) 已通过用户评审，代码实现恢复。首批范围为公共协议基础、接口草案和内存交易池；完整区块编码、解码及持久化闭环仍待后续实现。不得将本批报告为最小链已完成。

**后续优先级调整：** 用户要求先做出块。已增加构块、单节点共识、提交协调器、最小 KV 执行器与内存链存储，通过真实插件装配及出块集成测试。参见 [单节点内存出块](../block-production.md)。接下来优先补事务持久化和恢复；独立节点生命周期、完整网络解码与 HTTP API 仍未完成。

**持久化进展：** 根据用户要求先保证区块、状态、交易落盘，新增 storage/bolt，实现确认交易索引、同事务保存与重开验证，并覆盖提交前/后进程退出恢复。详见 [持久化存储](../storage.md)。待确认交易池仍为内存实现；不将本阶段报告为完整生产链。

## 已核对的上游依据

本机缓存的 PluginKit v0.1.6 源码提供 `build.Build[T](ctx, graph, rootID)`、`build.RequireByID[T]`。AgentKit 通过 `pluginkit.Register(kind, New)` 注册构造函数，配置指定实例与 deps。PluginKit v0.1.6 要求 Go 1.26.1，本机版本匹配。

第一版直接使用 PluginKit，不复制注册器和依赖图实现。构造函数不启动后台任务或打开长期持有的数据库连接，资源由节点生命周期管理。

## 执行规则

每个任务按以下步骤执行：增加明确行为测试；运行指定命令并记录预期失败；实现最小代码；重新运行并确认通过；运行相关回归；形成独立提交。所有文件路径均相对于仓库根目录。

此计划尚未执行，不把计划中的测试描述为已经通过。先交付公共类型与接口，再交付单个插件，最后集成。

## Task 1 公共协议与密码学

创建 `go.mod`、`core/types/hash.go`、`core/types/transaction.go`、`core/types/block.go`、`core/types/genesis.go` 和 `core/types/encoding_test.go`。

1. 建立 module `github.com/lengzhao/GoChainKit`，锁定 Go 版本和 PluginKit 依赖。
2. 先写测试：同一交易重复编码字节一致；修改链标识、序号或负载后签名验证失败；不合法公钥或签名长度返回错误而非 panic。
3. 运行 `go test ./core/types -v`，确认未实现功能导致失败。
4. 实现固定字段顺序、长度前缀的二进制编码，不使用 map 序列化作为签名输入。所有变长字段设大小上限；解码器拒绝多余尾部数据。
5. 使用 Ed25519 签名与 SHA-256；交易签名、交易哈希、区块哈希和状态根使用不同域前缀。序号使用无符号整数并校验溢出。
6. 通过测试后保存编码 golden vectors，明确它们是协议兼容性测试。

## Task 2 模块接口与运行协议

创建 `core/{lifecycle,transport,mempool,consensus,builder,executor,storage,api,crypto}.go` 和 `core/doc.go`。

1. 定义每个模块的输入、输出、错误类别、资源归属和并发约束，不在接口包依赖 PluginKit。
2. Consensus 采用受控驱动：Start 接收提议构建、验证及最终提交的宿主能力，Stop 终止其后台任务；Transport 的入站消息由协调器显式路由，不建立隐式反向依赖。
3. StateReader 与隔离状态视图分开；执行结果绑定父链头和候选内容，不向调用方暴露可修改的内部 map。
4. Mempool 的 Select 不移除交易。准入校验不取代最终执行校验，队列中的过期或冲突交易必须可移除。
5. 定义已知重复提交、父链头冲突、无效交易、资源耗尽、关闭及提交结果不确定的错误语义。
6. 运行 `go test ./core/...` 和 `go vet ./core/...`，确认公共类型与接口可构建。

## Task 3 有界交易池

创建 `plugins/mempool/{memory.go,memory_test.go,register.go}`。

1. 先测重复交易、队列满、按字节限额选取、选取后仍在队列、移除幂等、外部修改交易切片不影响池内容。
2. 执行 `go test ./plugins/mempool -v` 确认失败，再实现容量和大小限制、复制语义和互斥保护。
3. 用 `go test -race ./plugins/mempool` 验证并发 Add/Select/Remove。
4. 注册 `mempool/memory`，默认配置必须有界。

## Task 4 确定性键值执行器

创建 `plugins/executor/{executor.go,executor_test.go,register.go}`、`core/types/state_test.go`。

1. 先测正确签名与连续序号的写入、错误签名、跨链交易、重复序号、超大键值和取消。
2. 执行 `go test ./plugins/executor -v` 确认失败。
3. 实现按发送者地址隔离的键空间，明确键和值的长度上限；无效候选区块整体拒绝且不修改已提交状态。
4. 规范状态根按编码后的完整键进行排序，以域分离、长度前缀编码计算哈希；包含账户序号。
5. 不读取本地时间和外部数据；独立视图重放相同区块产生相同结果。用测试覆盖 map 插入顺序差异。

## Task 5 原子持久化与恢复

创建 `plugins/storage/local/{store.go,commit.go,recovery.go,store_test.go,register.go}`。

1. 核对所选数据库官方事务与同步落盘保证，锁定依赖；不得设置跳过同步的开发默认值。
2. 先测提交后查询一致、重复提交不重复写、旧父链头被拒绝、错误状态根被拒绝、不同 genesis 打开同一库失败。
3. 执行 `go test ./plugins/storage/local -v` 确认失败，再实现状态、区块、回执和链头在同一事务中提交。
4. 数据库写事务入口再次检查链头，避免执行与提交之间的竞态。
5. 在事务写入中注入测试故障，确认全部回滚；关闭重开后核对链头及状态。
6. 暴露只读快照及统一提交能力；装配时检测多个视图是否归属同一后端。对调用者返回的数据使用复制或明确的快照生命周期。

## Task 6 构块 单节点共识与节点生命周期

创建 `plugins/builder/`、`plugins/consensus/`、`plugins/transport/single/`、`node/` 下的实现、注册和测试文件。

1. 先测提交交易后产生区块、空交易池策略、区块大小限制、提交成功后移除交易、提交失败仍保留可重试交易。
2. 执行 `go test ./node ./plugins/builder/... ./plugins/consensus/...` 确认失败。
3. Node 串行协调最终提交；构块在隔离视图计算各类根，拒绝无效候选并明确剔除无效交易策略。
4. 单节点共识使用可取消 ticker，不模拟多节点安全性。传输最小实现明确报告没有对等节点。
5. 先写启动部分失败回滚、重复关闭、关闭超时、后台任务退出测试，再实现生命周期。
6. 运行 `go test -race ./node ./plugins/...`，检查数据竞争和关闭后的后台任务。

## Task 7 PluginKit 装配 配置与 HTTP API

创建 `plugins/all/all.go`、`assembly/{build.go,build_test.go}`、`plugins/api/http/`、`cmd/gochainkit/main.go`、`presets/single.yaml`。

1. 先写配置图构建测试：可成功取得 Node；未知插件、错误类型、循环依赖和不兼容共享存储被拒绝。
2. 执行 `go test ./assembly -v` 确认失败，再通过 PluginKit 的 Build 装配，禁止手工绕过配置图创建核心插件。
3. 默认监听本机回环地址，限制请求体与请求超时，不提供私钥托管、签名或生产公开写接口。
4. 使用 httptest 测试交易提交、状态、区块及回执查询；区分已入池和已确认。
5. 编写命令行参数、优雅关闭、数据目录和创世配置校验。配置不绑定本机绝对路径。

## Task 8 端到端验证与文档

创建 `tests/integration/{single_test.go,recovery_test.go}`、`README.md`、`docs/{protocol,plugins,quickstart}.md`。

1. 真实配置启动节点，使用测试客户端签名提交交易，等待确认，查询区块、回执和状态。
2. 停止并重启同一数据目录，确认链头、序号、状态根和数据一致。
3. 使用子进程模拟提交边界中断，验证恢复后不存在半提交可见状态；不要仅以正常 Close 代替崩溃验证。
4. 执行 `go test ./...`、`go test -race ./...`、`go vet ./...` 和 `go build ./cmd/gochainkit`。
5. 从 README 的命令进行一次独立烟雾验证，明确单节点开发限制、协议版本和插件扩展方式。

## 提交与交付

每个阶段按实际内容形成可审查提交；推送前复查工作区、测试结果和文档一致性。推送、PR 或其他外部发布只有在用户授权范围内执行。

第一阶段完成标准为公共协议、模块接口和对应验证；整个最小链完成标准为 Task 8 验收通过。两者分别报告，不混同进度。
