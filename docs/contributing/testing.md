# 扩展验证与交付

新增插件应有真实消费者、最小配置、替换语义及构造拒绝路径。规范见 [开发约定](../development-conventions.md)；本页给出验证入口，不把测试文件存在或历史记录当作本轮通过。

## 全仓基线

在仓库根目录：

```sh
go test ./...
go test -race ./...
go vet ./...
go test ./tests/integration -v
```

需要 Go 1.26.1、本机临时监听与依赖缓存。受限环境不能监听或取依赖时，应记录未验证范围并尊重原生权限，不修改全局信任/权限或伪造通过。

## 契约清单

| 范围 | 必须验证 |
| --- | --- |
| codec/协议 | 规范编码、golden vectors、任意二进制 ID、签名域、创世/规则身份、非法大小/字段 |
| 执行模型 | 确定性、父状态隔离、Writes/Deletes、nil/空值、错误分类、候选与历史视图一致 |
| 账本/后端 | 同 owner、原子提交、CAS、重复、ErrCommitUnknown/CommittedError、故障注入与重开 |
| 分支/共识 | 生产/导入/重放/恢复同规则、选链/最终性、回收交易、错误证明/跨链重放拒绝 |
| 插件/运行器 | 缺失/错类型/循环、双实例隔离、启动回滚、取消后等待退出、独占 Handler |
| 外部 Runtime | 实际确定性执行、计量版本、Artifact/代码绑定、状态和事件回滚；当前仍是未来验收 |

## 可定位的现有证据

- `tests/integration/assembly_test.go`：真实 PluginKit 与 `TestPersistentProductionGraph`。
- `tests/integration/production_test.go`：不同格式的生产/存储链路。
- `tests/integration/pow_network_test.go`：开发规模 libp2p 与 PoW。
- `protocol/composed/authority_test.go`：`TestAuthorityAccountUTXOReorgRecovery` 等。
- `tests/integration/authority_network_test.go`：`TestAuthorityThreeNodesHTTPForkAndObserverRecovery`。
- `plugins/storage`：写入失败、分支、恢复、历史格式等；`state/journal`：检查点/预算/借用快照。
- [首笔交易教程](../tutorials/first-transaction.md)：公开包、CLI、HTTP 与重开验证。

链开发者还应在自己的 module 验证公开 API；仓库 internal 测试包不可从外部导入。跨组合证据应列实际组合，不把 PoA×两模型通过写成 DPoS/任意共识通过。发布说明记录命令、版本、输出摘要、失败及未覆盖范围。
