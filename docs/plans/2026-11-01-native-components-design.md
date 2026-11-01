# BTC/ETH 原生组件薄封装

用户确认范围：复用成熟组件组装自定义服务；暂不接 keyring，也不承诺 BTC/ETH 网络兼容。

## 设计

`adapter/bitcoin` 保留 btcd 的 wire.MsgTx、OutPoint、TxOut 和 txscript 语义，提供有界严格编解码及按完整 prevout 集合验证脚本。`adapter/ethereum` 保留 go-ethereum 的 types.Transaction，提供有界编解码和绑定显式 chain ID 的签名发送者恢复。两者可由 Go 直接使用，不依赖账本、共识或插件注册。

各自插件只负责构造注册。独立工具 HTTP 服务以显式 BTC/ETH 能力依赖装配，可只启用一种；根组件实现 core.Node，通过现有 assembly.Load 和 CLI 运行。服务不收私钥、不广播交易、不持久化。请求大小、并发、超时、关闭均有界。原生格式与签名域不经过现有 Ed25519 钱包转换。

BTC 脚本验证要求调用方提供每个输入的 prevout；仅验证给定 prevout 的脚本，不证明真实存在、未花费或满足完整区块共识规则。ETH 恢复发送者要求正 chain ID 和受保护交易，拒绝错误链与未签名交易，不宣称状态执行验证。

## 验证

真实上游签名交易验证成功、篡改失败；严格编码往返、尾随数据、输入限额、错误链、取消；实际 PluginKit 配置装配与 HTTP 请求、启动关闭；执行测试、race 和 vet。
