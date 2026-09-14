# 原生 Computer Use：MCP 接入进度

当前交付：P2 截图与单次点击闭环。五个 computer 工具只在本机预编译 helper 握手成功后发布，同时宣告 `bridge.computer.v1`。Linux 和没有 helper 的安装保持普通工具可用；不会假装具备原生桌面能力。

## 部署约定

客户端、服务端和数据库按已确认方案统一重建部署；没有旧 computer 协议翻译、缺省权限兼容或数据库迁移分支。本次开发没有清空数据库、推送代码或部署服务。

用户安装的是预编译程序。Windows helper 使用 Go 调用系统 API，无需 Visual Studio、CMake；macOS helper 在构建机链接系统框架，终端用户无需 Xcode。已通过真机测试的 P0 校准程序保持原样。

## 执行链

`Nexus Target 授权 → Runtime 再校验 → 私有父子管道 → 独立 helper → OS API → 持久化结果 → 标准 MCP image`

helper 通过继承的匿名 stdin/stdout 管道通信，不监听 TCP、Unix socket 或 named pipe，不在磁盘存 IPC 口令。只有 Core 提供可信 owner，模型参数不能覆盖 WorkSession、Target、Deployment 或 revision。

同一 OS 用户的 helper 使用固定 OS 用户目录和排他文件锁，避免两个 Core 并行占用桌面。macOS 目录从 passwd 用户记录取得，Windows 从 Known Folder API 取得，不受 `AGENTDOCK_HOME`、`HOME` 或 `AppData` 环境变量切换影响。

会话租约为 15 秒；需显式 renew。观察有效期 30 秒，保存原始截图到原生坐标的变换及目标窗口/几何信息。截图大小是偏好，服务调整至传输预算并返回实际尺寸。MCP 图像在本次授权响应内传输，动作日志仅保存元数据和输入结果，不发布公共 Artifact URL。

点击前持久化 executing 状态，OS 返回后先保存输入结果，再抓取后图。相同 owner 和 operation_id 重读已存结果；参数不同时返回冲突。后图失败不重试点击。重启遇到 executing 记录返回 outcome_unknown，不能据此假定没有输入。

停止、租约到期、本地禁用、Target/WorkSession 撤销及 Deployment 变更会取消当前执行。停止处理与捕获任务分开；管道取消或关闭会终止 helper，重新连接不会重发任何调用。后续显式 status 可重读日志。

## 当前能力与边界

- 已实现 status、session acquire/renew/release、observe、单次 click、stop 和 operation_id 查询。
- AgentDock 面板可在后台。点击绑定截图时的前台目标窗口；失焦、窗口移动或点击落到其他窗口时重新观察，不检查窗口标题或工具链年份。
- status.actions 当前为 `["click"]`。多击、键盘、文本、滚轮及拖拽仍未实现，返回明确 unsupported；不会静默转成其他动作。
- Windows 使用 GDI 抓取当前显示画面、SendInput 提交一组移动/按下/抬起事件，报告 OS 接受数量。这是新的产品后端；P0 的 DXGI 真机结果不能当作此后端已经验证。缩放、旋转和多屏仍需本轮原生复测。
- macOS 使用 ScreenCaptureKit（13+）和 CGEventPost；仅报告 submitted，不宣称输入已被应用接受或业务成功。旋转显示器暂未实现。
- Windows helper 在当前交互用户的标准权限 Core 下运行；本轮不支持从 elevated Core 跨权限启动桌面 helper。锁屏、断开会话和安全桌面不执行输入。
- 本地 enable / stop / disable 为命令入口，尚未加入桌面面板开关。操作日志保留以便去重；当前不自动清理，图像元数据在 30 秒后回收。

## 本地启用与诊断

Windows：helper 与 `agentdock.exe` 放在同一目录，正式 Windows release zip / Setup 已增加这个文件。使用普通 PowerShell：

```powershell
.\agentdock-computer.exe enable
.\agentdock-computer.exe diagnose
```

`diagnose` 自动查询交互桌面、截图并打印结果，将图片存入该用户私有目录的 `diagnostic.png`，不点击鼠标。运行中的 Core 下一次维护周期即可识别 enable。若安装 helper 前 Core 已启动，需要重启 Core 以发现新工具。

macOS：正式包位于 `/Applications/AgentDock.app/Contents/Helpers/AgentDockComputer.app`。本地命令：

```bash
helper=/Applications/AgentDock.app/Contents/Helpers/AgentDockComputer.app/Contents/MacOS/agentdock-computer
"$helper" enable
"$helper" permissions
"$helper" diagnose
```

系统权限授予 **AgentDock Computer**；P0 校准程序的权限身份不同。permissions 是用户主动调用，MCP status 不触发系统授权弹窗。测试源码时由构建机执行 `bash packaging/macos/build-computer-helper.sh`；正式用户不执行构建。

任一平台使用 `agentdock-computer stop` 锁存本地停止，`enable` 才能解除。MCP 的 session acquire/renew 不能解除本地停止。`disable` 同时停止并关闭本地授权。

MCP 顺序：status 获取 desktop/display → session acquire → observe → act 使用截图像素坐标和新的 operation_id → status 重读该 operation_id → stop。Nexus Deployment 配置 observe 或 control；默认 none。出现 outcome_unknown 时先查结果，不换 operation_id 重试。

## 验证状态

Linux 自动化已覆盖引擎及真实子进程管道：重复操作、崩溃后结果恢复、所有权隔离、撤权、过期、边界坐标、停止后不恢复、后图失败及原生拒绝原因保留；所有成功结果实际经过共享 output schema 校验。MCP 测试确认图片转为标准 content:image，structuredContent 不携带 base64、路径或公共 URL。

本轮可以在 Linux 交叉编译 Windows Core/helper；macOS C/Objective-C 编译、两平台真实产品 helper 的输入测试和完整安装包测试仍须对应原生构建机执行，不能由 P0 测试或 mock 测试代替。

使用 `MCP/go.work` 关联本地 protocol。独立发布前仍需发布更新后的 protocol 模块并同步依赖版本；本轮没有推送或发布模块。
