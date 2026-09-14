# 内置能力开关

AgentDock 是节点配置的唯一真源。Windows 控制面板、macOS 高级设置中的 browser/ACP 复选框立即通过本地控制端点调用运行中的 Core；Nexus 的「运行环境 → Nodes → 内置能力」通过已有 `runtime.request` Bridge 修改明确指定的节点。离线时显示不可操作，不在 Nexus 排队或回放配置。

选择原子保存到 `AGENTDOCK_HOME/builtin-capabilities.json`。普通桌面设置保存只更新后端参数，不再保存开关，因此不会覆盖另一界面刚提交的选择。原有 `AGENTDOCK_BROWSER_ENABLED`、`AGENTDOCK_ACP_ENABLED` 和 `--browser-enabled` / `config update --acp-enabled` 已移除。首次启动默认关闭可选组；Docker browser 镜像首次启动时写入 browser 开启的初始选择，后续启动保留用户选择。

| 组 | 工具 | 当前发行策略 | 后端生命周期 |
| --- | --- | --- | --- |
| browser | browser_session、browser_act、browser_snapshot | 原生和 Docker 都提供 | 验证本地 Chromium 可执行文件或远程 CDP 只读握手；本地浏览器在建立会话时启动 |
| acp | acp_session、acp_prompt、acp_interaction | 原生提供，官方 Docker 排除 | 验证配置并与 adapter 握手；关闭中断 detached prompt、清理 interaction 和进程 |

文件、命令、Skills、任务和内置管理入口不受这些开关影响。外部 MCP 服务仍由 `mcp_manage` 及独立 MCP 页面管理，不是这里的内置工具组。开启组不授予 Deployment 权限；Full Access 不绕过用户关闭、后端不可用或发行排除。

## 实现入口与状态

- `internal/config.BuiltinProvided` 只定义发行策略。
- `internal/app/builtin.go` 串行化用户选择持久化、调用准入、取消域、后端清理和通知。每个 ToolSpec 的 `Group` 明确工具归属。
- `provided`、`enabled`、`ready` 相互独立。有效状态为三者均真且 `transitioning=false`。缺少 adapter、浏览器或远程 CDP 不使其他工具启动失败，界面显示原因。ACP adapter 意外断开也会撤下工具。
- 本地浏览器就绪检查验证可执行文件；实际启动仍可能因系统资源或浏览器参数失败，操作结果会保留具体错误。已连接的外部 CDP 只断开 AgentDock 连接，不杀用户浏览器。
- 关闭先禁止新调用并通知目录，再取消在途调用、停止后端，等已经准入的 handler 退出后完成切换。清理未完成时显示「正在切换」；失败显示原因，不报告可用。
- 重新开启建立新的取消域和后端实例，不恢复旧浏览器会话或 ACP prompt。已产生的副作用不回滚、不重放；ACP 关闭结果持久化为 `capability_disabled`，恢复后可通过 session inspect 重读历史。

## 通信与目录同步

`GET/POST /internal/runtime/builtins` 是 HTTP/Bridge 共用 Runtime 路径；POST 请求为 `{"id":"browser","enabled":false}`。桌面使用 `builtins.list` / `builtins.set` 本地控制方法，也可通过 `agentdock builtins list|set --runtime-root ...` 诊断。管理操作不作为模型工具公开。

MCP SDK 服务器保持同一个实例，增删工具会自动通知订阅客户端。Bridge 在 `node.hello` 及 `node.updated` 发送完整能力与工具快照，Nexus 用同一条汇总链重新计算目录。远程开关结果在快照发送后返回。重连以 AgentDock 当前配置重新声明，不依赖 Nexus 旧缓存。

当前 MCP 协议通过 `subscriptions/listen` 订阅 `tools/list_changed`；未订阅的客户端需自行重新列出工具，缓存的工具名仍会被节点调用门禁拒绝。Nexus 沿用既有离线节点契约保留策略，离线调用明确失败；重连后完整快照撤下不再提供的工具。Nexus 重启同样不会获得写入节点选择的权力。

## 后端参数与恢复

后端 CDP 地址、adapter 预设/命令/参数继续由原生高级设置和现有启动配置管理，修改这些参数仍需重启 Core，界面明确使用「应用并重启」。运行中的开关本身无需重启。修复后端后，Nexus 可使用「重新检查后端」；原生面板可关闭再开启。切换超时或连接中断时刷新节点状态核对结果，不自动重试操作。

验证覆盖调用准入与取消、并发切换、写入失败、重启持久化、ACP detached prompt 中断与历史、Docker 发行门禁、真实 HTTP MCP 订阅和 Bridge 完整快照。
