# GoChainKit

基于 Go 和 PluginKit 的模块化区块链开发框架，目标是通过配置装配模块，快速构建一条新链。

当前处于初始开发阶段，尚不能启动完整区块链，不适用于生产资产。

## 已实现

- 交易签名与验证、确定性交易编码、状态根基础算法。
- 区块、回执与创世数据类型，以及模块接口草案。
- 有界内存交易池及 PluginKit 注册，支持去重、预算选取和复制隔离。

## 开发

需要 Go 1.26.1，与当前 PluginKit 版本要求一致。

```sh
go test ./...
go test -race ./...
go vet ./...
```

## 设计文档

- [架构草图](docs/plans/2026-10-17-gochainkit-design.md)
- [编程与模块接口规范](docs/development-conventions.md)
- [实现计划](docs/plans/2026-10-17-gochainkit-implementation.md)

后续实现确定性执行器、事务存储、构块与单节点共识，最后接入配置装配、HTTP API 和恢复验证。
