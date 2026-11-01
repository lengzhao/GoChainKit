# Native Architecture Simplification Implementation Plan

**Goal:** 在不改变交易规则、现有插件Kind及HTTP协议的前提下收敛重复基础设施。

**Architecture:** runtime/http承载无账本HTTP生命周期和请求预算；api/http只管理链路由与owner校验。链内codec共享canonical解析，EVM同包stateSession管理隔离原生状态生命周期；按链注册包支持窄依赖编译，旧聚合包保留兼容入口。

**Tech Stack:** Go、net/http、PluginKit、btcd、go-ethereum。

### Task 1: HTTP宿主
- 新增runtime/http，先写有界请求/响应、并发、取消和生命周期测试确认失败。
- 提取现有链HTTP运行器，两种服务注入错误响应策略以保持wire兼容。
- api/http保留协议/账本绑定检查，service/transactions只负责工具路由。

### Task 2: 链内编解码与EVM职责
- adapter/ethereum共享canonical编解码和冻结原生交易。
- model/evm复用codec，保留London策略及原生签名域。
- 模型内部避免重复解码，Native返回副本保持不可变性。
- EVM同包拆分config/transactions/state/execution/query；stateSession统一恢复、释放、提交和预算，不新增跨链数据库接口。

### Task 3: 按链编译
- 拆分model、交易工具、native API注册及实现包。
- plugins/all聚合；旧Kind和旧import路径通过兼容聚合/薄别名保持可用。
- 用go list -deps确认BTC窄注册不会引入Ethereum包。

### Task 4: 审查与验证
- 复核现有路由、错误体、地址展示、关闭行为和交易策略兼容。
- go test ./...、相关race、go vet ./...。
- 现有HTTP提交、合约状态、重组、同步、恢复闭环全部保留。

不在本轮实施：增量链存储、新配置DSL、跨链万能模型。
