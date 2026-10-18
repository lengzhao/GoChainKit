# 密钥保存与身份适配

密钥保存、签名和链规则分层。`cap/keystore` 面向需要本地保存、可导出机密的消费者；`cap/signer` 面向签名消费者，不要求实现导出私钥。远程或硬件签名设备可以直接提供 Signer，无需实现本地 Store。

本轮实现最小保存能力及 libp2p 身份消费，不加入钱包、余额查询、交易构造、HD 派生、自动轮换、共识签名管理或 HTTP 密钥管理入口。

## 组合边界

- Store 按显式 ID 读取、独占创建和删除机密；返回数据为调用方拥有的副本。
- SecretSource 提供文件加密口令；配置只保存来源信息，不保存口令文本。
- 加密文件实现负责认证加密、受限文件访问与完整独占发布；不理解区块链交易或 libp2p 协议。
- libp2p 身份适配器负责生成、校验和取得该网络所需的私钥编码，通过 Store 持久化。
- network/libp2p 只接收身份能力、构造网络连接；不直接管理密钥文件。

构造函数不读取环境、不打开文件、不生成密钥。只有实际读取或创建时调用 SecretSource；缺少机密来源、错误口令或损坏记录必须失败，不能重新生成身份覆盖错误。

身份首次创建可能有竞争。创建必须是独占发布；落败者读取已经完整发布的身份，不能覆盖胜出者，也不能返回自己未保存的另一身份。

## 加密与记录

使用版本化记录，固定且有界的 scrypt 派生成本和 AES-GCM 认证加密。随机 salt、nonce 每次创建重新生成，认证数据绑定版本与记录 ID，避免把另一记录复制过来当成本记录。文件解析先检查格式及大小，不接受记录自行指定任意 KDF 工作量。

格式 1 使用 scrypt N=131072、r=8、p=1，派生 32 字节 AES 密钥；记录为版本字节、16 字节 salt、12 字节 nonce 和 GCM 密文。内置 Store 的机密上限 64 KiB，ID 上限 120 字节，文件名为 ID 原始字节的十六进制加 `.key`。这些是具体实现的限制，身份适配器只要求非空引用，不把文件名限制强加给其他 Store。同一加密文件 Store 串行执行 KDF，等待期间遵守 context；KDF 本身不可中断，固定成本限制单次工作量。

派生密钥与口令用完后尽力清理；Go 运行时和第三方库可能产生额外副本，这不构成受保护内存或硬件隔离的保证。返回私钥编码属于显式可导出身份能力，不扩散成所有 Signer 的公共方法。

文件内容完全写入、同步并关闭后才发布最终名称。新目录和文件分别限制为 0700、0600；非法 ID、路径逃逸及超大记录被拒绝。错误消息不得包含口令或私钥。不会自动重写用户已有文件。

加密算法 API 依据：[Go scrypt](https://pkg.go.dev/golang.org/x/crypto/scrypt)、[Go AEAD/GCM](https://pkg.go.dev/crypto/cipher#NewGCM)。协议格式、参数和行为以本仓库实现及测试为准。

## 现有数据边界

三个 PoW 预设采用以下依赖链，目录分别为 data/pow-keys、data/pow-account-keys、data/pow-utxo-keys：

```yaml
key-secret:
  use: secret/env
  config: {environment: GOCHAINKIT_KEYSTORE_PASSWORD}
keys:
  use: keystore/encrypted-file
  config: {directory: data/pow-keys}
  deps: {secret: key-secret}
identity:
  use: identity/libp2p
  config: {keyID: p2p}
  deps: {store: keys}
network:
  use: network/libp2p
  config: {listen: [/ip4/127.0.0.1/tcp/19001], seeds: []}
  deps: {protocol: protocol, identity: identity}
```

运行前通过运行环境提供 GOCHAINKIT_KEYSTORE_PASSWORD。缺失口令只在实际使用时失败；不含网络的 single 配置不要求口令。既有保存目录权限也必须仅允许所有者访问，不会自动收紧用户目录权限。`keystore/memory` 仅用于临时开发与测试，退出后不保留身份。

账本后端抽象保持磁盘格式 2；机密保存变更与链规则无关。旧版 network 的明文 identityPath 文件不会自动转成加密记录或被覆盖。已有网络身份需要显式导入后再切换配置；没有导入时，新保存位置生成的是新 PeerID，原种子地址不能据此视作同一身份。本轮不提供隐式迁移。

导入由应用程序完成：读取旧文件的 libp2p 私钥编码，验证编码后调用新 Store.Create(ctx, "p2p", material)。目录、口令来源与运行配置保持一致，导入成功后启动节点核对 PeerID。Create 不覆盖已有记录；不要为了导入先删除未知的现有密钥。

保存目录与口令来源需要分别备份与恢复。删除记录仅删除文件，不承诺在日志文件系统或备份介质上物理擦除密钥。
