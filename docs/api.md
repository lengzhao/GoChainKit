# 外部 API

API 分为领域服务、HTTP 传输和可选模型路由。`core` 不增加外部访问方法；应用可以替换传输层，复用同一受理与查询逻辑。

## 领域服务

`api.Service` 通过 `plugins/api/query` 注册为 `api/query`。Deps 只接收协议编解码与无状态校验、一个只读账本 owner，以及交易池。构造不打开数据库或监听端口。

提交先按协议编码和解码冻结交易对象，再做无状态校验和有界交易池受理。`accepted` 仅表示本地交易池受理，不承诺上链、最终性或永久保留；状态相关的余额、序号和输入可花费性检查仍由执行器负责。已确认交易重复提交返回观察到的 `confirmed`。

`cap/query.ConfirmationReader` 在同一锁或数据库读事务中取得 canonical 交易索引、可选回执和观察链头，随后在锁或事务外解码。查询响应描述一次观察，不承诺返回后不会重组。没有回执与回执载荷为空是不同情况。

状态查询使用明确区块 ID；省略锚点时只读取一次链头，然后打开该区块的不可变快照。调用方负责关闭快照，并保留关闭错误。通用 API 不提供任意状态键或全状态扫描。

## HTTP 契约

| 请求 | 语义 |
| --- | --- |
| `POST /v1/transactions` | 接收已签名的协议交易编码；新受理返回 202，已确认返回 200 |
| `GET /v1/chain/head` | 返回一个明确链头的区块编码与状态承诺 |
| `GET /v1/blocks/{id}` | 返回指定区块的原协议编码 |
| `GET /v1/transactions/{id}` | 返回 `pending` 或 `confirmed` 及原协议编码；未知返回 404 |
| `GET /v1/receipts/{id}` | 返回回执、包含区块与观察链头；无回执或未确认返回 404 |
| `GET /v1/node` | 返回协议身份与可选网络连接信息 |

提交体为严格 JSON，拒绝未知字段和额外 JSON 值：

```json
{"data":"<Base64 编码的协议交易字节>"}
```

所有 ID 和状态承诺按原始字节编码为十六进制，不要求 32 字节；协议编码和回执载荷使用 Base64。金额、序号等 uint64 值在 JSON 中使用十进制字符串，避免客户端丢失整数精度。通用响应不解析默认交易头或模型字段。

链头响应为 `{id,data,stateRoot}`，区块为 `{id,data}`。交易响应包含 `{id,data,status,index}`，已确认时另含 `blockId` 和 `head`；`index` 是块内位置字符串，pending 时不表示确认位置。回执响应为 `{id,data,blockId,index,head}`。提交响应为 `{id,status,blockId,head}`，尚未确认时后两项为空字符串。

`GET /v1/node` 返回 `protocol:{name,version,network}`；注入网络能力时另有 `network:{addresses,peers}`。它不暴露节点私钥或本地数据路径。

错误响应采用统一结构：

```json
{"error":{"code":"...","message":"..."}}
```

输入错误、未找到和容量限制与本地故障分开。混合错误中的真实故障不能因同时包含取消或未找到而被掩盖；内部错误响应不暴露数据库路径或底层错误详情。

| HTTP 状态 | error.code |
| --- | --- |
| 400 | `invalid_input` |
| 404 / 405 | `not_found` / `method_not_allowed` |
| 408 / 504 | `canceled` / `timeout` |
| 413 | `too_large` |
| 422 | `invalid_transaction` |
| 429 | `capacity` |
| 503 | `closed` |
| 500 | `internal`，包括响应预算超限和未知本地故障 |

协议编解码拒绝作为纯输入边界报告 400；校验器的未知错误保留为本地故障。HTTP 超时后客户端仍应按 ID 查询结果；请求取消不意味着已经完成的受理被撤销。

## 装配与资源限制

`api/http` 的 Deps 为 `query`、可选 `network` 和可选 `modules` 路由列表。`cap/http.RouteProvider` 只贡献路由；服务器统一应用请求上下文、并发限制和响应预算。模型路由没有自己的监听端口。不同账本的模块或服务不能混装到同一个节点。

API 同时配置为根节点的 `resources` 和 `workers`：前者管理监听的启动与关闭，后者监督服务故障。节点取消会撤销活动请求，关闭过程等待处理函数退出后才释放账本。扩展路由必须遵守请求取消，不得自行保留请求或 ResponseWriter 启动脱离生命周期的任务。

下面是已有 protocol、ledger、pool、host、consensus 的配置图中新增的部分：

```yaml
api-query:
  use: api/query
  deps: {protocol: protocol, ledger: ledger, pool: pool}
api:
  use: api/http
  config: {listen: '127.0.0.1:8080'}
  deps: {query: api-query}
node:
  use: node/default
  deps:
    ledger: ledger
    host: host
    consensus: consensus
    resources: [api]
    workers: [api]
```

有 P2P 网络时，可给 `api` 添加 `network: network` 依赖，并保留 `resources: [network, api]`。省略 API 插件的配置仍可运行。

默认监听 `127.0.0.1:8080`，CLI 打印实际 API 地址。测试或多实例可配置 `127.0.0.1:0` 由系统分配端口。

| 配置字段 | 默认值 |
| --- | --- |
| `maxRequestBytes` | 2 MiB + 1024 字节（包含 Base64 和 JSON 开销） |
| `maxResponseBytes` | 8 MiB |
| `maxIDBytes` | 4096 原始字节 |
| `maxConcurrent` | 64，超过立即拒绝 |
| `readHeaderTimeout` | 5s |
| `readTimeout` / `writeTimeout` | 15s |
| `idleTimeout` | 30s |
| `requestTimeout` | 10s，业务操作必须遵守 context |
| `shutdownTimeout` | 5s，超时后强制关闭连接 |

领域服务另有 `maxTransactionBytes`，默认 1.5 MiB；HTTP JSON/Base64 开销也计入请求预算。调整协议交易或区块尺寸时，应同步考虑交易池、构块、网络同步和 API 预算。

## 可选模型查询

账户和 UTXO 路由由独立插件贡献，通用 HTTP 服务通过类型化 Deps 接收路由能力列表，不按模型名称分支。模型适配器验证协议实际使用的执行模型，并与查询服务绑定同一账本。

- `GET /v1/accounts/{address}`：账户余额与序号。
- `GET /v1/utxos/{transaction}/{index}`：指定输出；不存在或已花费返回 404。

两者支持 `?at=<十六进制区块 ID>`，响应包含实际查询锚点。地址长度和输出引用格式由具体模型适配器校验，不变成通用 API 的限制。

账户响应为 `{at,address,balance,nonce}`，不存在的地址返回零余额和零序号。UTXO 响应为 `{at,transactionId,index,owner,amount}`；花费前的历史输出仍可通过旧区块锚点读取。金额、序号和输出位置均使用十进制字符串。

`api/account` 与 `api/utxo` 的 Deps 均为 `protocol`、`query`。`cap/model.Provider` 仅向模型专属适配器暴露实际执行引擎；通用 HTTP 与核心执行接口不要求这项能力。例如在账户配置图中新增：

```yaml
api-model:
  use: api/account
  deps: {protocol: protocol, query: api-query}
# api 的 deps 加上 modules: [api-model]
```

内置四个启动配置分别使用 HTTP 端口：single 8080、pow 8081、pow-account 8082、pow-utxo 8083。复制配置启动多个节点时，同时调整 P2P/API 监听端口及数据库、身份路径。

启动后可直接读取链头与节点信息：

```sh
curl http://127.0.0.1:8080/v1/chain/head
curl http://127.0.0.1:8080/v1/node
```

提交时在客户端按所选协议签名并编码交易，再将编码字节转成 Base64 放入 `data`。API 不生成钱包、不接收私钥，也不把普通 JSON 业务对象自动转换成协议交易。

本轮不提供钱包签名、管理接口、合约调用、订阅、按所有者枚举 UTXO 或历史账户索引。
