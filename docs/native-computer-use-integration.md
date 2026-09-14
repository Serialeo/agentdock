# 原生 Computer Use：MCP 接入进度

当前交付：截图、鼠标移动、多击、拖拽、滚动、快捷键和 Unicode 文本输入的 MCP 接入。五个 computer 工具只在本机预编译 helper 握手成功后发布，同时宣告 `bridge.computer.v1`。Linux 和没有 helper 的安装保持普通工具可用；不会假装具备原生桌面能力。

## 部署约定

客户端、服务端和数据库按已确认方案统一重建部署；没有旧 computer 协议翻译、缺省权限兼容或数据库迁移分支。本次开发没有清空数据库、推送代码或部署服务。

用户安装的是预编译程序。Windows helper 使用 Go 调用系统 API，无需 Visual Studio、CMake；macOS helper 在构建机链接系统框架，终端用户无需 Xcode。已通过真机测试的 P0 校准程序保持原样。

## 执行链

`Nexus Target 授权 → Runtime 再校验 → 私有父子管道 → 独立 helper → OS API → 持久化结果 → 标准 MCP image`

helper 通过继承的匿名 stdin/stdout 管道通信，不监听 TCP、Unix socket 或 named pipe，不在磁盘存 IPC 口令。只有 Core 提供可信 owner，模型参数不能覆盖 WorkSession、Target、Deployment 或 revision。

同一 OS 用户的 helper 使用固定 OS 用户目录和排他文件锁，避免两个 Core 并行占用桌面。macOS 目录从 passwd 用户记录取得，Windows 从 Known Folder API 取得，不受 `AGENTDOCK_HOME`、`HOME` 或 `AppData` 环境变量切换影响。

会话租约为 15 秒；需显式 renew。观察有效期 30 秒，保存原始截图到原生坐标的变换及目标窗口/几何信息。截图大小是偏好，服务调整至传输预算并返回实际尺寸。MCP 图像在本次授权响应内传输，动作日志仅保存元数据和输入结果，不发布公共 Artifact URL。

输入前持久化 executing 状态，OS 返回后先保存输入结果，再抓取后图。相同 owner 和 operation_id 重读已存结果；参数不同时返回冲突。后图失败不重试输入。重启遇到 executing 记录返回 outcome_unknown，不能据此假定没有输入。

停止、租约到期、本地禁用、Target/WorkSession 撤销及 Deployment 变更会取消当前执行。停止处理与捕获任务分开。普通取消通过管道通知 helper，等待当前动作释放按键或鼠标，不直接杀进程；关闭管道也先取消并等待清理。只有进程未能在 4 秒内退出才强制终止并报告执行状态不确定，不能保证强杀后已释放输入。重新连接不会重发任何调用；后续显式 status 可重读日志。

## 当前能力与边界

- 已实现 status、session acquire/renew/release、observe、act、stop 和 operation_id 查询；status.actions 为 `["move","click","drag","scroll","key","text"]`。
- AgentDock 面板可在后台。默认整屏观察允许点击其他窗口、任务栏或 Dock 来切换应用；仅显式传入 window_id 才约束到该窗口。动作开始前若前台目标或截图几何已改变，需要重新观察；不检查窗口标题或工具链年份。
- 拖拽发送连续轨迹，支持拖动窗口或应用内对象。拖动自身引起的窗口位置变化不会中断动作；目标消失、桌面锁定或焦点切换到无关窗口会取消。拖拽起终点均使用所观察屏幕的截图坐标。
- 点击支持左、中、右键和 1–3 次点击。快捷键支持 Ctrl、Cmd、Alt/Option、Shift、Win、方向键和功能键；修饰键先按、反序释放。CommandOrControl 自动选择当前平台的主要修饰键，别名和大小写不会改变含义。
- text 直接发送 Unicode，支持中文、emoji、换行和制表符，不借用剪贴板。文本动作分小批次检查取消；原文不写操作日志，参数摘要使用本机私有密钥的 HMAC。
- 正常取消、超时及关闭会配对释放本动作已经按下的键和鼠标，不补发尚未提交的事件。OS 强杀或拒绝释放时结果标记不确定。
- Windows 使用 GDI 抓取当前显示画面、SendInput 提交鼠标和键盘事件，报告 OS 接受数量。这是新的产品后端；P0 的 DXGI 真机结果不能当作此后端已经验证。缩放、旋转和多屏仍需本轮原生复测。
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

系统权限授予 **AgentDock Computer**；P0 校准程序的权限身份不同。permissions 是用户主动调用，MCP status 不触发系统授权弹窗。macOS helper 由发布构建机生成并随客户端提供，安装和使用不需要原生编译。

任一平台使用 `agentdock-computer stop` 锁存本地停止，`enable` 才能解除。MCP 的 session acquire/renew 不能解除本地停止。`disable` 同时停止并关闭本地授权。

MCP 顺序：status 获取 desktop/display → session acquire → observe → act 使用截图像素坐标和新的 operation_id → status 重读该 operation_id → stop。Nexus Deployment 配置 observe 或 control；默认 none。出现 outcome_unknown 时先查结果，不换 operation_id 重试。

## 动作示例

以下是 computer_act 的 action 字段；外层仍需 session_id、observation_id 和新的 operation_id。需要确认动作效果时重新 observe，键盘和文本发送到当前前台应用。

```json
{"kind":"move","point":{"x":300,"y":200}}
{"kind":"click","point":{"x":300,"y":200},"button":"left","count":2}
{"kind":"drag","point":{"x":300,"y":100},"to":{"x":600,"y":300},"duration_ms":500,"button":"left"}
{"kind":"scroll","point":{"x":500,"y":400},"delta_x":0,"delta_y":-120}
{"kind":"key","keys":["CommandOrControl","Shift","S"]}
{"kind":"text","text":"你好，AgentDock 🙂"}
```

滚动 delta_y 正数向上、负数向下，delta_x 正数向右、负数向左；具体距离由平台和应用解释。快捷键也接受 `["Ctrl+Shift+S"]`；加号键写作 Plus。macOS 字母/符号快捷键当前按 ANSI 键位映射，文本输入使用 Unicode。每次拖拽最长 3 秒，同一个动作内部完成按下与释放，不跨请求维持按住状态。

## 验证状态

Linux 自动化已覆盖动作轨迹、坐标变换、快捷键顺序与部分提交后的释放、Unicode 分批、取消拖拽后释放、真实子进程取消与 EOF 后清理；另覆盖引擎及管道的重复操作、崩溃后结果恢复、所有权隔离、撤权、过期、边界坐标、停止后不恢复、后图失败及原生拒绝原因保留；所有成功结果实际经过共享 output schema 校验。MCP 测试确认图片转为标准 content:image，structuredContent 不携带 base64、路径或公共 URL。

Windows helper 已在 Linux 交叉编译通过。macOS C/Objective-C 尚未在 macOS SDK 环境编译验证；新增动作也尚未通过两平台产品 helper 真机测试。原生构建与完整安装包验证由发布流程完成，P0 校准测试和模拟测试不能代替这些结果。

使用 `MCP/go.work` 关联本地 protocol。独立发布前仍需发布更新后的 protocol 模块并同步依赖版本；本轮没有推送或发布模块。
