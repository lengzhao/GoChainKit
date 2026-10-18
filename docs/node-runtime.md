# 节点运行与配置

命令行入口为 cmd/gochainkit，默认读取 presets/single.yaml。配置只包含 root 与 plugins，插件实例、依赖引用与构造均由 PluginKit 负责；assembly 不维护另一套注册表。配置大小上限 1 MiB，拒绝未知顶层字段和多文档输入。插件内部配置约束由 PluginKit 与各构造函数处理。

node/default 依赖同一账本 owner 的 Ledger、BoundHost 和 Consensus，可选 resources 与 workers。Ledger 若实现 Lifecycle 会先启动；resources 按配置声明的依赖顺序启动，同一可比较实例只启动一次。资源应使用稳定指针身份，Start 失败须清理该资源自身的部分初始化；根节点逆序停止此前成功启动的资源。

额外资源或 worker 若实现 LedgerBound，也必须绑定根节点的同一 LedgerIdentity。协议描述符相同不代表两个数据库是同一账本。

Run 是单次调用的阻塞运行入口。共识与所有 worker 均受监督；主动取消会通知全部任务并等待它们返回，任务提前返回或报告内部错误会取消其他任务，错误返回调用者。所有任务必须遵守 context 取消契约，运行器无法强制终止不合作的 goroutine。任务退出后才关闭资源，避免后台任务访问已关闭数据库。

提交成功后的交易池清理使用独立的 5 秒预算，不被原请求取消中断；若清理仍失败，返回 CommittedError 并保留已提交事实。

停止使用独立 context，默认总预算 5 秒，可配置 shutdownTimeout。Stop 必须遵守该预算；错误会聚合上报。生命周期回调不在运行器内部锁中调用。账本由运行器拥有时，调用方不得同时手工启动或关闭它。

默认开发配置在没有交易时仍产生空区块以验证链头推进。HTTP API 通过 api/query 与 api/http 插件提供交易受理和查询；API 必须同时加入 resources 与 workers，关闭请求后再释放账本。CLI 打印实际监听 URL，详见 [外部 API](api.md)。未配置 API 的自定义配置仍可运行，程序也可通过装配结果获取 Mempool 提交交易。单节点共识不提供拜占庭容错、分布式共识或生产安全保障。

```sh
go run ./cmd/gochainkit --config presets/single.yaml --run-for 5s
```

相对数据库路径基于进程工作目录；重复运行会恢复已有账本。接收到中断或终止信号时正常关闭。自定义二进制可导入所需插件包并调用 assembly.Load，再调用返回的 Node.Run。
