# 原生 Computer Use P0：真机测试指南

这份工具包包含两个独立的本地验证 App。它们只向自己的绿色校准区域发送一次鼠标点击，并保存点击前后的显示器截图和 JSON 报告。不会安装服务、连接 MCP 或上传报告。

当前状态：Linux 上的 C++ 几何测试与 Python 报告检查测试已通过。用户在安装 VS 2026 的 Windows 真机上反馈基本验证、缩放与角落测试正常，旋转屏确认暂不支持；macOS 已根据两轮构建反馈补齐 CoreGraphics 导入，并将启动入口改为显式主 Actor 入口，等待重新构建及真机验证。Windows 结果来自用户反馈，尚未收到或检查原始 JSON／截图。源码包不是预编译安装包。如果第一步编译失败，保留完整错误输出即可，不需要继续点权限或做桌面测试。

## 1. 把源码放到两台机器

将 `agentdock-computer-p0-source.zip` 分别复制到 Mac 和 Windows，解压到普通用户可写的目录。以下命令都在解压后的 `agentdock-computer-p0` 根目录执行；完整 agentdock 仓库也可以使用相同相对路径。

先做每个平台的“基本闭环”即可；通过后再做下面的补充矩阵。报告中的 screenshot 是**整个待测显示器**，请先整理桌面。截图保存在本机。

## 2. Mac：构建并打开

要求：macOS 13+，Apple Silicon 或 Intel；安装支持 macOS 13 SDK 的 Xcode Command Line Tools。第一次可在 Terminal 执行 `xcode-select --install`，等待系统安装完成。

在源码根目录执行：

```bash
bash desktop/macos/AgentDockComputer/build.sh && open dist/computer-spike-macos/AgentDockComputer.app
```

脚本先在临时目录构建并运行几何测试，再编译 App；编译成功后才创建或更新 `.app` 并本地签名。上面的 `&&` 保证构建失败时不会继续打开 App。默认 ad-hoc 签名仅供本机测试；若已有有效签名身份，可通过 `AGENTDOCK_COMPUTER_SIGN_IDENTITY` 指定。此次不验证 Developer ID、公证或正式升级签名链。

打开后：

1. 点“检查权限”，记录辅助功能、屏幕录制的初始状态。
2. 点“请求权限”，在系统设置的“隐私与安全性”中给 **AgentDockComputer** 开启“辅助功能”和“屏幕录制”（某些系统显示为“屏幕与系统音频录制”）。只需这两项。若列表未出现，可添加生成的 `.app`。
3. 完全退出测试 App，用上面的 `open` 命令重新打开。点“检查权限”，应显示两项为 `true`。
4. 将整个绿色区域放在一块未旋转的显示器内，使 App 保持前台。点“运行一次验证”，松开鼠标，等待约 3–10 秒。
5. 预期绿色区域变蓝，界面显示 `PASS`。点“打开报告目录”，查看最新子目录中的三个文件。

保持 `.app` 路径不变，同一构建退出重开后再运行一次，以确认权限保持。修改源码重新构建可能改变 ad-hoc 代码身份，此时可能需要移除旧权限条目、重新添加 App；这不能算正式签名身份持久性已通过。

报告位置：

```text
~/Library/Application Support/AgentDock/computer-spike/<本次 UUID>/
```

## 3. Windows：构建并打开

要求：Windows 10/11 x64，使用本机登录的普通桌面用户。安装 **Visual Studio 2022 或 2026**（完整 IDE 或 Build Tools 均可），选择“使用 C++ 的桌面开发”，包含 MSVC x64、Windows SDK 和 C++ CMake 工具。

在源码根目录的 PowerShell 中执行：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\desktop\windows\computer-helper\build.ps1 -Run
```

`ExecutionPolicy Bypass` 仅用于这次脚本进程。脚本自动识别已安装且带 C++ x64 工具链的 VS 2022／2026，选择支持对应构建器的 CMake，构建 Release 并运行几何测试；`-Run` 在成功后打开 App。运行 App 无需管理员权限。

VS 2026 需要 CMake 4.2+；VS 2022 需要 CMake 3.21+。脚本优先检查所选 VS 自带的 CMake，再检查 PATH 中的 CMake。若提示没有兼容的 CMake，在 Visual Studio Installer 更新 C++ CMake 工具，或安装新版 Windows CMake 并加入 PATH。脚本按 VS 版本和安装实例使用独立构建目录，不需要删除旧缓存。去掉 `-Run` 则仅构建，末尾会打印可执行文件的完整路径。依据：[CMake VS 2026 构建器说明](https://cmake.org/cmake/help/latest/generator/Visual%20Studio%2018%202026.html)。

打开后：

1. 将整个绿色区域放在一块未旋转的显示器内，使 App 保持前台。
2. 点 `Run once`，松开鼠标，等待约 3–10 秒。程序会通过 DXGI 截图、`SendInput` 点击，再取新截图。
3. 预期绿色区域变蓝，界面显示 `PASS` 及本次报告路径。
4. 在资源管理器地址栏输入 `%LOCALAPPDATA%\AgentDock\computer-spike`，按修改时间找最新子目录。

Windows 不使用 macOS 那样的辅助功能授权开关。本测试针对当前用户会话中的自身普通窗口；不会弹出提权，也不验证对管理员窗口、UAC 安全桌面的控制。

## 4. 看什么结果，怎么反馈

一次成功报告包含：

- `report.json`：平台与执行组件身份、显示器/图像坐标、输入状态、帧时间、目标事件及颜色检查。
- `before.png`：校准区为绿色的整屏截图。
- `after.png`：同一区域为蓝色的新截图。

`PASS` 同时要求系统输入被提交（Windows 还检查接受的事件数）、自身校准区收到带标记的事件、新帧中采样点由绿变蓝。它证明这次自己的目标区点击闭环，不代表任意应用可控、中心像素精度或业务任务完成。截图检查是一个离开点击中心的采样点，仍请人工确认前后图中的校准区位置正确。

如已安装 Python 3，可进一步检查报告内部一致性：

```bash
python3 desktop/computer-spike/check_report.py "/完整路径/report.json"
```

Windows 可将 `python3` 换成 `py -3`。返回码 `0` 表示报告内部一致的 PASS，`1` 表示原生验证未通过，`2` 表示报告缺失或不一致。工具检查时间顺序、事件/颜色字段、PNG 文件头/尺寸以及文件哈希；不会重新计算图片颜色，也不提供签名或真实性证明。

请反馈每台机器的 **系统版本、机型/显卡、显示器分辨率和缩放比例、构建是否成功、界面结果**，并附 `report.json` 或其中的错误。截图可先自行检查；需要排查坐标问题时再提供已确认可分享的截图。失败也有价值，不要手动把 `passed` 改为 true。

## 5. 基本闭环通过后再做这些测试

每种条件单独点一次 Run，每次都有独立报告目录。

| 场景 | 操作与预期 |
| --- | --- |
| 同一构建重启 | 退出重开 App，再运行；Mac 检查两项权限仍为 true。 |
| 取消 | 点 Run 后，在 3 秒准备期内点 Stop；应未通过，`input_attempted=false`，不能出现自动补点。 |
| Mac 权限未授予 | 初次不授权直接 Run，应未通过且未发送输入。若要测撤销权限，关闭 App 后撤销其中一项，再打开并运行，之后恢复。 |
| 副屏/负坐标 | 把窗口完整移到副屏；特别测试设置在主屏左侧或上方的屏幕。运行期间保持布局不变。 |
| 不同缩放/DPI | Mac 分别测内置 Retina 和现有外屏；Windows 分别测现有的 100%、150% 等缩放。记录实际设置。 |
| 失焦 | 在准备期切换其他 App，应停止于前台/遮挡检查，不自动找回焦点重发点击。 |
| 锁屏/恢复 | 准备期锁屏，解锁后查看结果；不应在恢复后自动发送点击。失败后仅在用户再次 Run 时重建采集。此项用于暴露会话恢复风险。 |
| 旋转屏 | 当前明确返回“不支持”；记录未覆盖，不算通过。 |

Stop 取消后续工作，不能撤回已经提交的那次点击；native API 或 GPU 调用本身不承诺能被立即打断。运行中强制退出可能来不及写报告，该次不能记为通过。不要把本 P0 的窗口按钮等同于设计中的独立停止开关。

## 真机结果记录

本轮用户反馈原文：“windows测试没有问题，旋转屏确实还不支持，我也测试了，缩放没问题，角落测试也没问题”。测试反馈发生在提供 VS 2026 兼容修复包（提交 `609c679`）之后，实际运行构建的版本标识尚未核对。

| 项目 | 已知结果 | 证据与范围 |
| --- | --- | --- |
| Windows 基本验证 | 用户真机反馈通过 | 环境已知安装 VS 2026；尚未收到构建日志、`report.json` 或截图。 |
| Windows 缩放 | 用户真机反馈通过 | 具体缩放比例、显示器及系统版本未提供，不外推到所有 DPI 配置。 |
| Windows 角落 | 用户真机反馈通过 | 保留用户对“角落测试”的描述，具体摆放位置未提供，不等同于全屏每点精度验证。 |
| Windows 旋转屏 | 用户已测试，确认暂不支持 | 已覆盖当前“不支持”的行为；旋转屏输入功能仍未实现。 |
| macOS | 两轮构建错误已分别修复，待重试 | 首轮为 CoreGraphics 导入缺失；第二轮为非隔离顶层入口调用 `@MainActor SpikeApp()`。已补齐导入并使用 `@main` + `@MainActor` 入口和 `-parse-as-library`，尚无完整构建成功及点击闭环结果。 |

Windows 的取消、锁屏/会话恢复、独立停止及崩溃恢复没有逐项反馈，仍保持待验证。上面的记录不代表完整 P0 门槛已通过。

## 6. P0 的边界与后续门槛

本提交交付验证工具，**不宣称 P0 真机门槛已通过**。正式设计见同目录 `native-computer-use-design.md`（精简源码包可能只包含本指南）。

接入 MCP 前仍需取得两平台原生构建及基本闭环报告，修复真机发现的问题，并补完签名身份、会话锁定/切换、独立停止、helper 崩溃和恢复等验证。lease、IPC、持久动作日志、未知结果不重放、正式截图存储和证据关联尚未在该原生工具中实现。已有任务步骤重开与证据关联不受本工具影响。

实现依据：[Apple ScreenCaptureKit 示例](https://developer.apple.com/documentation/screencapturekit/capturing-screen-content-in-macos)、[Chromium 对帧 contentRect 和 scaleFactor 的处理](https://chromium.googlesource.com/chromium/src/+/refs/heads/main/content/browser/media/capture/screen_capture_kit_device_mac.mm)、[Microsoft SendInput](https://learn.microsoft.com/en-us/windows/win32/api/winuser/nf-winuser-sendinput)、[Microsoft AcquireNextFrame](https://learn.microsoft.com/en-us/windows/win32/api/dxgi1_2/nf-dxgi1_2-idxgioutputduplication-acquirenextframe)。
