# Native Components Implementation Plan

**Goal:** 以薄适配器复用 BTC/ETH 交易能力，并通过配置装配独立服务。

**Architecture:** 原生类型留在具体适配包，插件仅构造注册；工具服务注入小接口，不依赖链账本。

**Tech Stack:** btcd wire/txscript、go-ethereum core/types、PluginKit、net/http。

### Task 1: 原生适配器

- 新建 adapter/bitcoin 与 adapter/ethereum。
- 先写真实签名、严格往返、篡改和边界测试，运行确认缺少实现失败。
- 实现 Decode/Encode、BTC VerifyScripts 和 ETH Sender；固定依赖版本。
- 运行 go test ./adapter/...。

### Task 2: 服务及装配

- 新建 service/transactions 及 plugins/transactions。
- 先写 httptest 与 assembly.Load 集成测试，确认缺少实现失败。
- 显式注入 BTC/ETH 能力；提供独立 core.Node HTTP 根、请求限制及优雅关闭。
- 新建 presets/transactions.yaml，聚合注册新增插件。

### Task 3: 验证与说明

- 记录接口、路由、输入格式、校验边界和运行命令。
- 运行 go test ./...、相关 race、go vet ./... 和 CLI 有界运行。
- 检查 diff，保留无关用户文件。
