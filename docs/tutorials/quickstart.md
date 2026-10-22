# 启动一条开发链

要求：仓库工作副本、Go 1.26.1；第一笔交易教程还使用 curl、Python 3。以下只使用临时数据，不打开仓库下已有 `data/`。single 不需要口令或 P2P 身份。

在仓库根目录执行：

```sh
GCK_REPO="$PWD"
GCK_DEMO="$(mktemp -d "${TMPDIR:-/tmp}/gochainkit-demo.XXXXXX")"
go build -o "$GCK_DEMO/node" ./cmd/gochainkit
(cd "$GCK_DEMO" && ./node --config "$GCK_REPO/presets/single.yaml" --run-for 5s)
```

预期打印 HTTP 监听地址，默认 `127.0.0.1:8080`，并有界运行后退出。数据库在 `$GCK_DEMO/data/chain.db`。端口占用会失败，不应停止未知进程；复制 preset 到临时目录，显式修改 `api.config.listen`，然后同步调整客户端 URL。

重新执行最后一行会打开同一账本继续出块；不要在同一数据库上并行启动两个进程。更换 shell 时先恢复上述两个变量的原值，不要创建新临时目录冒充重开验证。

下一步：[第一笔签名交易](first-transaction.md)，使用同样的临时目录方法从头完成提交、确认及重开查询。多节点、口令与种子配置见 [网络](../network-consensus.md)；PoA 使用 [专门初始化器](../govm-foundation.md)。
