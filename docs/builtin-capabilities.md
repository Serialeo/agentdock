# 内置能力开关

AgentDock 是节点配置的唯一真源。Windows 控制面板、macOS 高级设置中的 browser/ACP 复选框立即通过本地控制端点调用运行中的 Core；Nexus 的「运行环境 → Nodes → 内置能力」通过已有 `runtime.request` Bridge 修改明确指定的节点。离线时显示不可操作，不在 Nexus 排队或回放配置。

选择原子保存到 `AGENTDOCK_HOME/builtin-capabilities.json`。普通桌面设置保存只更新后端参数，不再保存开关，因此不会覆盖另一界面刚提交的选择。原有 `AGENTDOCK_BROWSER_ENABLED`、`AGENTDOCK_ACP_ENABLED` 和 `--browser-enabled` / `config update --acp-enabled` 已移除。首次启动默认关闭可选组；Docker browser 镜像首次启动时写入 browser 开启的初始选择，后续启动保留用户选择。

| 组 | 工具 | 当前发行策略 | 后端生命周期 |
| --- | --- | --- | --- |
| browser | browser_session、browser_act、browser_snapshot | 原生和 Docker 都提供 | 接收浏览器请求；默认后端检查只提供诊断，具体后端在建立会话时验证 |
| acp | acp_session、acp_prompt、acp_interaction | 原生提供，官方 Docker 排除 | 验证配置并与 adapter 握手；关闭中断 detached prompt、清理 interaction 和进程 |

文件、命令、Skills、任务和内置管理入口不受这些开关影响。外部 MCP 服务仍由 `mcp_manage` 及独立 MCP 页面管理，不是这里的内置工具组。开启组不授予 Deployment 权限；Full Access 不绕过用户关闭、后端不可用或发行排除。

## 实现入口与状态

- `internal/config.BuiltinProvided` 只定义发行策略。
- `internal/app/builtin.go` 串行化用户选择持久化、调用准入、取消域、后端清理和通知。每个 ToolSpec 的 `Group` 明确工具归属。
- `provided`、`enabled`、`ready` 相互独立。有效状态为三者均真且 `transitioning=false`。ACP 的 `ready` 表示 adapter 握手成功，断开后撤下工具。browser 的 `ready` 表示请求服务可以接收调用：默认浏览器或 CDP 故障会显示诊断，但保留工具，允许 `browser_session.start` 按调用提供 `cdp_url`。实际请求仍检查发行策略、用户开关和 Deployment 权限。
- 本地浏览器就绪检查验证可执行文件；实际启动仍可能因系统资源或浏览器参数失败，操作结果会保留具体错误。已连接的外部 CDP 只断开 AgentDock 连接，不杀用户浏览器。
- 关闭先禁止新调用并通知目录，再取消在途调用、停止后端，等已经准入的 handler 退出后完成切换。清理未完成时显示「正在切换」；失败显示原因，不报告可用。
- 重新开启建立新的取消域和后端实例，不恢复旧浏览器会话或 ACP prompt。已产生的副作用不回滚、不重放；ACP 关闭结果持久化为 `capability_disabled`，恢复后可通过 session inspect 重读历史。

## 通信与目录同步

`GET/POST /internal/runtime/builtins` 是 HTTP/Bridge 共用 Runtime 路径；POST 请求为 `{"id":"browser","enabled":false}`。桌面使用 `builtins.list` / `builtins.set` 本地控制方法，也可通过 `agentdock builtins list|set --runtime-root ...` 诊断。管理操作不作为模型工具公开。

MCP SDK 服务器保持同一个实例，增删工具会自动通知订阅客户端。Bridge 在 `node.hello` 及 `node.updated` 发送完整能力与工具快照，Nexus 用同一条汇总链重新计算目录。远程开关结果在快照发送后返回。重连以 AgentDock 当前配置重新声明，不依赖 Nexus 旧缓存。

当前 MCP 协议通过 `subscriptions/listen` 订阅 `tools/list_changed`；未订阅的客户端需自行重新列出工具，缓存的工具名仍会被节点调用门禁拒绝。Nexus 沿用既有离线节点契约保留策略，离线调用明确失败；重连后完整快照撤下不再提供的工具。Nexus 重启同样不会获得写入节点选择的权力。

## 后端参数与恢复

后端 CDP 地址、adapter 预设/命令/参数继续由原生高级设置和现有启动配置管理，修改这些参数仍需重启 Core，界面明确使用「应用并重启」。运行中的开关本身无需重启。修复后端后，Nexus 可使用「重新检查后端」；原生面板可关闭再开启。切换超时或连接中断时刷新节点状态核对结果，不自动重试操作。

请求排队等待支持 deadline；选择已持久化后，请求超时只结束客户端等待，后台继续取消与清理，在旧调用全部退出前保持 `transitioning=true`，不替换后端。关闭 Core 会先取消所有组，再等待已接受的切换和调用退出。忽略取消的 handler 仍可能延迟整个关闭；不能通过提前释放后端指针来伪装清理完成。

启动采用同步、有限时的可选后端检查：默认 CDP 探测最多 5 秒，ACP 握手最多 10 秒，随后才开始监听；探测失败仅影响诊断或 ACP 就绪状态。状态文件读取或 JSON 解码失败选择直接报错退出，避免猜测并覆盖用户选择。恢复时先备份文件，再修正其 JSON；也可移走损坏文件并通过正常管理入口重新选择。Core 不自动隔离或重写损坏文件。

## stdio 实例管理

stdio 与 HTTP 共用本地控制入口。stdio 默认使用 `<AGENTDOCK_HOME>/runtime/stdio`，不继承桌面进程的 `AGENTDOCK_RUNTIME_ROOT`；也可给服务器显式传入 `--runtime-root`。管理命令必须使用对应的目录，例如在一个终端或 MCP 客户端中启动：

```sh
AGENTDOCK_HOME="$HOME/.agentdock-stdio" agentdock --stdio
```

在另一个终端管理该实例：

```sh
agentdock builtins list --runtime-root "$HOME/.agentdock-stdio/runtime/stdio"
agentdock builtins set --runtime-root "$HOME/.agentdock-stdio/runtime/stdio" --id browser --enabled=true
agentdock builtins set --runtime-root "$HOME/.agentdock-stdio/runtime/stdio" --id acp --enabled=true
```

每个并行 Core 使用独立 home 和 runtime root。进程持有 home 与控制端点的独占锁，第二个实例明确启动失败，不能接管首个实例的控制入口。Unix 上选择较短的 runtime root，以符合系统 socket 路径长度限制。

## 统一升级步骤（破坏性升级）

Linux/macOS 的运行时和共享存储锁现由内核 `flock` 保护，修复版进程被强杀后可以在同一 home 上重新启动。锁使用持久化的 `*.lock.flock` 文件；文件存在不代表进程仍占用，运行期间不得删除或替换这些文件。文件系统不支持 `flock` 时启动会明确失败。

从 `0.9.2` 升级前请先正常停止所有使用该 home 的旧实例，使旧目录锁释放。若旧实例已经被强杀并留下仅记录 PID 的 owner 文件，新版无法证明该 PID 在其他容器命名空间中已退出，因此不会自动删除。必须先确认没有任何进程或容器使用该 home，再检查启动错误指出的锁目录；仅清理确认属于已退出旧实例的 `owner-*` 文件和空锁目录。不要删除整个 home、业务数据或未知锁内容。已发布的 `0.9.2` 二进制不包含此修复。

本次交付以 AgentDock `v0.9.4`、NexusDock `v0.9.4` 和共享协议 `v0.10.0` 配套版本统一重部署，不迁移旧开关，不支持新旧节点混合运行。两个消费者统一引用共享协议 `v0.10.0`。热更新管理的最低支持版本为配套的 `v0.9.4`，不能只用连接协议版本号判断支持情况。

1. 停止旧 AgentDock 与 Nexus，备份需要保留的节点 home、身份及用户数据。数据库的清理或重建由部署人员按统一部署方案执行。
2. 清理启动脚本、服务定义中的 `--browser-enabled`、`config update --acp-enabled` 以及环境文件中的 `AGENTDOCK_BROWSER_ENABLED`、`AGENTDOCK_ACP_ENABLED`。后端地址、命令、参数仍保留。旧 CLI 参数不再被接受，旧环境开关不再起作用。
3. 准备同一批次的共享协议、Nexus 和 AgentDock 构建，先启动 Nexus，再启动全部新版节点。完成这一批次前不要开放工作流流量，也不要连接旧节点。
4. 首次启用通过原生 GUI、Nexus 节点管理或上述 CLI 完成，不手工预置内部文件。已存在的新状态文件继续作为唯一真源；普通首次启动默认关闭。Docker browser 镜像只在文件不存在时初始化为开启，用户关闭后的容器重启和升级保持关闭，需持久挂载节点 home。
5. 检查节点目录和两组实时状态，再恢复工作流。用户重新开启能力不恢复旧会话或授予额外权限。Computer Use 源码及专用 worker 已移除；外部能力通过通用动态 MCP 接入。

验证覆盖调用准入与取消、并发切换、写入失败、重启持久化、ACP detached prompt 中断与历史、Docker 发行门禁、真实 HTTP MCP 订阅和 Bridge 完整快照。
