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
