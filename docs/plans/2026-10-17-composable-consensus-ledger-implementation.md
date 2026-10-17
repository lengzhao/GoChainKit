# Composable Consensus and Ledger Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 用规则级PoW、DPoS与UTXO、账户模型的四种真实运行组合验证框架可组合性。

**Architecture:** 状态变更、账本执行、区块定稿、共识验证/封印与选链/最终性分离；配置绑定组合身份，存储保留原子性与恢复验证。

**Tech Stack:** Go 1.26.1、PluginKit、bbolt、go-libp2p。

## Task 1：删除与可替换账本策略
Files: core/contracts.go、core/pow.go、新能力文件、plugins/storage/helpers.go、branch.go及测试、plugins/builder/basic.go。
先以失败测试明确删除不同于空值、重组与Bolt恢复保留删除语义；实现统一变更应用与持久记录。将分支保存/原子主链切换与工作量策略解耦，旧配置默认策略保持兼容；拒绝不兼容策略及最终性回退。普通/race测试和独立规格、质量审查后提交。

## Task 2：独立账本模型
Create model/account/*、model/utxo/*（实际路径按工程边界确定并更新本计划）。先写签名转账、守恒/溢出、nonce、双花、删除、手续费与系统奖励测试；实现模型与codec，模型不可导入PoW/DPoS包。共用执行上下文提供高度、费用接收与系统操作，结果拥有复制语义。

## Task 3：规则级PoW与组合协议
Create rules/pow/*、protocol/modular/*、plugins/protocol组合注册等。先写目标编码规范性、历史预期难度、调难边界、时间检查、累计工作量、奖励与手续费测试。定稿和seal分离，使用同一PoW实现分别组合账户及UTXO。真实网络、重组、磁盘恢复通过。

## Task 4：DPoS
Create rules/dpos/*、plugins/consensus/dpos*、必要的投票日志。先落实确定性选举、权益锁定与激活、轮次出块/投票、阈值证书验证、防重复/双签与重启恢复测试。明确安全/活性假设，不能用仅有轮值签名的实现替代已要求的投票确认。两种模型共用DPoS规则与运行逻辑。

## Task 5：组合验收
Create tests/integration/composition_matrix_test.go与四个preset，更新README/接口/设计说明。四种组合走真实转账、libp2p、多节点确认、Bolt重开；恶意目标/无效票/双花/跨配置旧库明确拒绝。全仓race、vet、gofmt、CLI实际启动验证；最终审查后本地提交，不自动合并或推送。

统一验证命令：env GOTOOLCHAIN=local GOCACHE=/private/tmp/gochainkit-cache GOMODCACHE=/private/tmp/gochainkit-modcache /opt/homebrew/bin/go test -race ./...；真实本机libp2p端口测试需要已授权的外层执行环境。实现中发现契约调整时，在不扩大目标的前提下同步文档与验收。
