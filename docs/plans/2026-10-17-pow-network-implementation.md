# PoW Network Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 配置启动支持分叉重组与追赶的 libp2p + 简化 PoW 开发网络。

**Architecture:** 保留格式无关核心，以可选工作量协议和分支账本能力组合；网络只传有界协议字节。根运行器监督共识与网络任务，共享同一账本 owner。

**Tech Stack:** Go 1.26.1、PluginKit、bbolt、go-libp2p。

## Task 1：PoW 协议与分支账本
Files: core/pow.go、protocol/powkv/*、plugins/storage/branches*.go、plugins/protocol/powkv.go。
先写失败测试覆盖固定目标验算、创世绑定、分支状态隔离、严格累计工作量切换、同权保留、原子索引替换、重开恢复；运行指定包测试确认失败。实现格式中立工作量契约、示例协议、内存与Bolt的分支能力；本地重算每块工作量，拒绝重复链上交易与未知父块。普通及race测试通过后独立规格、质量审查并提交。

## Task 2：libp2p 传输
Files: plugins/network/libp2p.go、libp2p_test.go、go.mod/go.sum。
先写两个真实host loopback测试，覆盖持久身份、点对点请求、超限、超时、取消清理。实现可配置监听、身份文件、种子节点、链身份握手、有界消息传输；网络类型留在插件内部。go test -race ./plugins/network 通过并审查。

## Task 3：多节点PoW运行
Files: plugins/consensus/pow.go、tests/integration/pow_network_test.go、presets/pow.yaml、plugins/all/all.go。
先写三节点竞争、断网分叉重连、缺失祖先追赶及磁盘重开测试；实现挖矿取消、接收验算、分支导入、广播与周期重试追赶，所有任务由Node监督；坏远端消息不能杀死节点，内部存储故障仍上报。主链重组后重新检查并恢复旧分支独有交易。

## Task 4：最终验收与文档
README、docs/network-consensus.md：说明配置、操作、格式兼容性和开发范围；全仓 go test -race ./...、go vet ./...、gofmt及真实CLI多进程启动验证。审查所有未解决项，最终本地提交；不自动合并主分支。

所有Go检查使用 env GOTOOLCHAIN=local GOCACHE=/private/tmp/gochainkit-cache /opt/homebrew/bin/go。

## 执行记录

- Task 1：PoW 协议与内存/Bolt 分支账本已实现，规格和质量审查通过，相关 race 测试通过；本地提交 b456925。
- Task 2：真实 libp2p 传输、原子持久身份和双向资源预算已实现，规格和质量审查通过，真实双节点 race 测试通过；本地提交 3bf92ad。
- Task 3：分页追赶、重组、重启、自动矿工、最大交易传播与停机故障监督均完成，两阶段审查与真实网络 race 验证通过。
- Task 4：操作文档、CLI地址输出、最终全仓 race / vet / gofmt / diff 检查均通过；两个独立 CLI 矿工运行后，从磁盘重开为观察节点，经种子连接追赶到相同链头和状态根。整体范围核对后收尾提交。

## 最终验收说明

全仓 race 测试通过，集成部分约 24 秒。真实网络覆盖 35 块较弱分支追赶 70 块主链（首批 32 块不足以切换仍可推进）、三自动矿工竞争、最大合法 1 MiB value 的传播与确认、Bolt 重开与相同 head/Root/Entries。严格更重规则在等权时不强制收敛，测试使用后续 settling block 打破平局。
