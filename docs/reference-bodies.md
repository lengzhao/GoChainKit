# 消息路由、引用式正文与高度查询

2026-10-30。第一阶段实现说明。完整正文是 composed 的默认模式；引用式正文需要显式配置 `referenceBody: true`，模式进入协议和创世绑定，不能用原完整正文链的数据目录启动引用链。

## 启动与装配

```sh
# 使用独立数据目录；密钥口令由环境 GOCHAINKIT_KEYSTORE_PASSWORD 提供。
go run ./cmd/gochainkit --config presets/pow-reference.yaml
```

[完整配置](../presets/pow-reference.yaml) 使用独立的正文归档和账本 Bolt 文件。装配方向为：

- `protocol/composed-pow`：账户模型、PoW、显式引用体模式。
- `archive/transactions`：依赖 protocol 和独立 Backend；不依赖账本、交易池或网络。
- `body/local`：依赖 References、交易 codec 与 sources: [archive]。
- `storage/kv`：依赖 protocol、账本 Backend、archive、materializer: body-local。
- `body/remote`：依赖 protocol、network、router，提供远端 TransactionSource。
- `body/fetch`：依赖 References、交易 codec 与 sources: [archive, body-remote]。
- `importer/branch`：额外注入 materializer: body-fetch，在候选验证/执行前补齐冷区块。
- `body/server`：依赖 protocol、archive 和 router，既是资源也是受监督 worker。
- `query/height` 与 `api/height`：可选的锚定高度查询及 HTTP 路由。

`body/local` 与 `body/fetch` 复用同一有界装配算法，通过明确的 Sources 区分数据来源。账本的 Materializer 必须只接本地持久来源；不要将 body-fetch 配到账本读回路径。普通完整正文协议无需配置归档和补齐器。

## 消息路由与生命周期

`message/router` 独占 Network Handler。`sync/chain` 使用 `chain/sync/v1`，正文服务使用 `body/transaction/v1`。路由帧为大端 uint16 路由字节长度、路由名和载荷；响应是该路由的原始响应字节。路由名要求版本后缀；重复、未知、非法路由均失败。

同步服务现在必须显式注入 Router。已有预设、开发网模板和 PoA 初始化器已更新；自定义配置需添加：

```yaml
router: {use: message/router, deps: {network: network}}
# sync/chain.deps 增加 router: router
# node.resources 在 network 后加入 router
```

新消息封装与旧版直接同步 JSON 不互通，同一开发网的节点需要一起升级；完整正文协议的交易、区块和创世编码保持原有默认语义。

引用链资源顺序：

```yaml
resources: [archive, ledger, network, router, body-server, api]
workers: [api, body-server]
```

根节点在 Resources 显式包含 Ledger 时尊重声明顺序；未列出 Ledger 时保留默认先启动账本的行为。资源全部启动后才运行共识和其他 workers；停止先取消并等待 workers，再逆序关闭资源。归档先于账本启动，后于账本关闭。Router 和路由注销会取消并等待在途处理器，Stop 后不能再注册或请求；同一实例不支持停机后重启。

默认 Router 预算为请求/响应各 6 MiB、入站和出站分别 32 个并发、每请求 5 秒。底层 Network 的帧预算仍独立生效。并发门控在出站分配帧前执行，入站和出站各用独立门控，避免回环请求占满同一门控而死锁。

## 正文与持久化不变量

引用模式的线上区块只携带有序交易 ID，完整交易仅保留在内存执行对象中。补齐校验交易 ID、顺序、规范编码和正文承诺；补齐前后 Block.ID 与规范线上编码不变。未补齐对象不能作为空交易执行；引用模式拒绝线上附带的 Transactions 数组（包括空数组）。

本地构块器直接生成带辅助正文的候选；Coordinator 冻结时保留并重新核对这些正文。远端冷区块由 Importer 使用 body-fetch 补齐，随后照常验证和执行。账本在验证候选后、保存引用块前持久化其全部正文，再从本地归档解码补齐并复核；归档失败不能推进链头。两个数据库各自事务提交，失败时可能留下无区块引用的正文，但不能留下已提交区块缺正文。

正文归档与 mempool、主链确认交易索引独立。交易池驱逐、主链脱离和重组均不删除归档；已存侧链所需正文也保留。当前不提供归档 GC，达到配额后明确失败，需要运维扩容，不能直接删除数据库中的记录。

归档保存格式版本、ProtocolDescriptor、精确创世 ID/编码、初始状态承诺和使用量。启动重新校验所有正文及配额统计。默认配额为 1 GiB 正文编码、1,000,000 笔交易、单笔 2 MiB；不含数据库元数据和文件空间开销。Put 校验规范编码和身份，对同一 ID 的相同字节幂等，对冲突内容拒绝。

本地补齐用于账本冻结、读回、分支规划、策略历史视图及恢复路径。引用式 composed 采用分支存储，启动重验全部已存分支，不只校验当前主链。缺正文或损坏正文导致启动失败；运行时已存在区块缺正文标为本地数据不可用，不能伪装成区块不存在。Archive 读取故障及 Importer 补齐时的本地故障进入监督；远端缺失、非法正文和网络超时仍按远端失败处理。正文服务通过 Run 将本地 Source 故障报告给节点监督。

补齐器默认每块最多 4096 个引用、4 MiB 正文、8 个并发装配、整次装配 30 秒；远端单次交易获取默认最多尝试 16 个 peers、总计 5 秒。所有来源只在纯 NotFound 时回退；混合故障不会被当成正常缺失。业务回调遵守 context，超时不是可强制终止任意插件代码的隔离机制。

## 锚定高度 API

`query/height` 沿指定 anchor 的祖先查找，不读取当前 canonical head；支持历史头和侧链头。它是祖先遍历适配器，目前不是独立持久化的常数时间高度索引。默认单次最多访问 100,000 个区块，可通过 `maxDepth` 配置；超过预算明确失败。

按需向 HTTP modules 加入 `api/height` 后：

```text
GET /v1/chain/heights/12?at=<区块ID的十六进制编码>
```

`at` 必填，固定查询历史。响应包含 `at`、十进制字符串 `height`、区块 `id` 和 Base64 协议编码 `data`。不存在的 anchor 或目标高度返回 NotFound；本地存储/正文损坏返回内部错误。并发链头变化不会改变同一 anchor 的查询含义。

## 验证入口

```sh
go test ./message/router ./archive/transactions ./body/...
go test ./node ./plugins/storage ./plugins/replication
go test ./query/height ./api/http/height
go test ./protocol/composed -run 'TestReference'
go test ./tests/integration -run TestReferencePresetProductionAndReopen
```

覆盖真实网络在双方交易池均不含正文时的获取、确认及断网重开；另有主/侧链正文恢复、缺正文启动失败、运行期缺正文分类、归档写入失败不推进链头、精确创世绑定、取消启动、关闭等待与规范编码回归测试。全仓测试仍用于验证完整正文模式的网络、PoA、钱包和开发网回归。
