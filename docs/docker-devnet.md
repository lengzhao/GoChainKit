# 三节点 BTC 风格开发网络

本网络使用 GoChainKit 自己的 Ed25519 交易、组合式区块格式与 libp2p 通信，提供 UTXO 货币、签名转账、手续费、PoW、奖励成熟期、减半、难度调整、累计工作量选链及分叉恢复。没有创世预分配，资产由挖矿产生。

## 一键启动

需要 Docker Engine 与 Docker Compose v2，支持 Linux amd64/arm64。宿主机不必安装 Go。

```sh
docker compose up -d --build --wait
docker compose ps
docker compose logs -f node1 node2 node3
```

首次启动的 init 服务生成三个独立矿工钱包和 P2P 身份，保存在各自数据卷的加密 keystore 中；生成共享创世和节点配置，并将其他两台节点的 DNS/PeerID 写入 seeds。init 正常退出后，三个节点自动启动、连接、挖矿和同步。无需手工抄 PeerID。

API 仅映射到宿主机回环地址：node1 为 `http://127.0.0.1:8081`，node2 为 `http://127.0.0.1:8082`，node3 为 `http://127.0.0.1:8083`。P2P 在 Compose 内网使用 19001。健康检查验证本地链 API 就绪；端到端验收进一步验证互联和共识。

如果端口已被占用：

```sh
GOCHAINKIT_PORT1=28081 GOCHAINKIT_PORT2=28082 GOCHAINKIT_PORT3=28083 \
  docker compose up -d --build --wait
```

纯本地演示默认口令为 `development-only-change-me`。可以在首次启动前通过环境变量 `GOCHAINKIT_KEYSTORE_PASSWORD` 指定口令，也可以放入不提交 Git 的 `.env` 文件。此后必须使用相同口令；改变环境变量不会重新加密已有密钥。口令不进入 YAML、镜像或钱包命令参数。

重复启动会校验原配置和身份并继续使用旧链；配置被修改、口令错误、部分初始化或缺失密钥时会明确失败，不覆盖已有材料。检查问题后可使用新的 Compose project 名创建另一套网络。每个项目有四个独立卷：共享配置与三个节点数据卷。正常停止使用 `docker compose down`，数据卷保留；不要对需要保留的链使用删除卷选项。

## 钱包与转账

钱包仅在本地解密和签名，HTTP API 不接收私钥。金额均为最小整数单位，`100000000` 单位相当于本开发链的 1 个币。

查看 node1 矿工地址与余额：

```sh
docker compose exec -T node1 gochainkit-wallet address --keystore /data/node1/keys
docker compose exec -T node1 gochainkit-wallet balance --keystore /data/node1/keys --api http://127.0.0.1:8080
```

在 node2 上创建收款钱包（`create` 拒绝覆盖同名密钥）：

```sh
docker compose exec -T node2 gochainkit-wallet create --keystore /data/node2/keys --key recipient
```

将返回的 64 位十六进制地址填入转账命令：

```sh
docker compose exec -T node1 gochainkit-wallet send \
  --keystore /data/node1/keys --api http://127.0.0.1:8080 \
  --address <收款地址> --amount 100000000 --fee 1000
```

钱包从同一链头的分页快照中选择已成熟输入，自动找零。返回 `accepted` 表示进入交易池；等待后续区块，并通过返回的交易 ID 查询确认：

```sh
docker compose exec -T node1 gochainkit-wallet transaction --api http://127.0.0.1:8080 --id <交易ID>
docker compose exec -T node2 gochainkit-wallet balance \
  --keystore /data/node2/keys --key recipient --api http://127.0.0.1:8080
```

同一钱包连续转账时，等待前一笔确认再发送下一笔。钱包当前只选择已确认输入，没有本地未确认输入预留；并发发送可能选择同一个 UTXO。

`gochainkit-wallet sign` 从标准输入读取显式构造的未签名 UTXO JSON，输出规范编码的 `id` 和 Base64 `data`，用于离线签名或协议验证。该命令不访问节点、不提交交易；签名有效不代表输入存在、已成熟或金额合法，这些由节点执行验证。

重复提交同一已确认交易返回原确认信息。交易池准入进行编码和签名检查；所有输入的存在性、所有权、成熟期、金额守恒与双花检查在构块和远端区块重执行时完成。准入成功不保证最终确认。内存交易池不跨全部节点停机持久保存；未确认交易可由钱包/客户端重播，已确认状态由 Bolt 保存。

也可在宿主机用 `go run ./cmd/gochainkit-wallet` 运行同样命令，并指定相应 API。跨钱包/设备转账无需共享 keystore；`balance --address <地址>` 支持无私钥查询。

## 共识与奖励规则

开发参数：初始奖励 `5000000000` 单位；从高度 150 开始奖励减半，后续每 150 块再减半；高度 `h` 的奖励在候选高度 `h+5` 可花费；每 20 块按 2 秒目标间隔调整难度，实际时间跨度按四倍上下界约束，目标不超过 `0x207fffff`。创世不发行奖励。三台节点都独立挖矿，本地挖矿调度分别约为 2、3.1、4.3 秒；同步轮询为 200ms。调度不属于共识规则，实际出块时间受竞争、执行与难度影响。

coinbase 采用**确定性系统输出**：执行器计算补贴加手续费，根据链绑定、父块、区块高度和矿工地址生成唯一铸币标识，并在同一次状态提交中创建奖励 UTXO 和成熟期元数据。客户端无法提交铸币交易，也不能自报奖励金额。节点重放会核对整个状态根；侧链奖励随分支状态保存，重组时原子切换。奖励可通过地址 UTXO 查询的 `coinbase`、`createdHeight`、`matureHeight` 字段识别；它不是交易列表里的 Bitcoin coinbase 交易。

这些参数用于快速演示。BTC 的生产参数、交易编码、脚本、地址和网络协议不在本网络的兼容范围内；不支持 Bitcoin Script、SegWit、Bitcoin 钱包互联或主网接入。机制参考见 [Bitcoin Developer Guide](https://developer.bitcoin.org/devguide/block_chain.html)。

## 查询 API

原有接口继续可用：`GET /v1/node`、`GET /v1/chain/head`、`GET /v1/blocks/{id}`、`POST /v1/transactions`、`GET /v1/transactions/{id}`、`GET /v1/receipts/{id}` 和单个 UTXO 查询。

新增接口：

```text
GET /v1/addresses/{64位十六进制地址}/utxos?limit=128&offset=0
GET /v1/addresses/{地址}/utxos?at={区块ID}&limit=128&offset=128
```

返回 `at`、`height`、`spendable`、`immature`、`coins` 和 `nextOffset`。高度/金额/输出索引使用十进制字符串，避免 JavaScript 精度损失。总余额覆盖该快照的全部 UTXO，`coins` 只返回当前页，`limit` 为 1–256，`nextOffset=0` 表示结束。后续页必须传回首次返回的 `at`；内置钱包自动处理。`spendable` 按 `height+1` 计算，表示下一块可花费的金额，不含交易池内未确认转账。链头响应的 `data` 是 Base64 编码的协议区块。

当前按地址查询会扫描完整快照，适合开发数据量；没有地址索引、裁剪、SPV 或公共 RPC 鉴权。

## 自动验收

镜像构建后执行（需要 Python 3，只有标准库依赖）：

```sh
python3 scripts/devnet-smoke.py
# 使用独立项目与其他端口，保留结果供检查
python3 scripts/devnet-smoke.py --project gochainkit-acceptance --base-port 28081 \
  --report /tmp/gochainkit-devnet-report.json
```

脚本会自动启动网络，创建新收款钱包、转账并检查各节点确认与余额，检查精确手续费奖励、提交未成熟 coinbase 花费并验证其确认高度、验证双花无法进入主链、拒绝错误签名，重启 node2，停止 node3 验证追赶，再临时断开 node3 的 Docker 网络，使两侧产生分叉并验证重连收敛。退出时会恢复其断开的网络，不删除数据卷，也不停止网络。请在开发网络执行。使用 `--skip-start` 可验收已启动的匹配项目。

Go 检查：

```sh
go test ./...
go test -race ./...
go vet ./...
```

模型/协议专项测试额外覆盖成熟边界、无上下文拒绝、手续费、越权/超额/双花拒绝、减半与调难边界、分叉状态回滚和落盘重开。

已完成的一次真实网络运行及验证边界见 [2026-10-24 验收记录](validation/2026-10-24-three-node.md)。

## 企业代理 CA

如果本机代理 CA 没有进入 Docker BuildKit，模块下载可能报告 `x509: certificate signed by unknown authority`。可向构建器提供包含所需公开 CA 的 PEM 文件：

```sh
docker build --secret id=go_ca,src=/path/to/trusted-ca.pem -t gochainkit:dev .
docker compose up -d --wait --no-build
```

证书只在构建阶段临时用于模块下载；保持 TLS 和 Go 校验数据库验证开启。普通环境直接使用标准构建命令。
