# 原生 Computer Use：MCP 接入进度

当前交付：P1 共享契约、权限和路由边界。原生 IPC、执行服务及打包仍属于下一阶段，当前不会在 MCP 中发布五个 computer 工具或宣告 `bridge.computer.v1`。

## 部署约定

按用户确认，客户端、服务端和数据库统一重新部署。本轮没有旧 Node 字段裁剪、缺失权限字段归一化、旧 computer 数据迁移或混合版本降级。每个 Deployment 明确携带 `computer: none | observe | control`，新建项目界面默认 `none`。本次开发没有清空正在使用的数据库。

正式用户安装的是包含预编译原生 helper 的客户端，运行 computer use 不需要 Visual Studio、CMake、Xcode 或 Swift 编译器。这些仅是构建机依赖；P0 源码测试包与正式安装包是不同交付物。本轮保留已真机测试的两个 P0 App 及其构建脚本。

## 已落地

- protocol 提供五个 Node 工具的输入／输出 schema、动作与观察类型、权限枚举和错误码；不加入 Nexus 自有 canonical 工具列表。
- Nexus 新库的 Deployment 使用 `computer_permission` 列；Target 快照带显式 computer 字段。界面、HTTP 契约、保存／回填／展示同步。
- Nexus 在转发桌面工具前检查实际 computer 能力及 Deployment 权限。Full Access 可以覆盖项目权限，不能把不存在的后端变成可用。
- AgentDock 增加相同权限分类。历史 Target 仅允许本人 stop 和带 operation_id 的已存结果查询；实时观察与新动作不享受历史豁免。Bridge、Runtime 与 Nexus 使用同一分类函数。
- 当前 Node 尚无正式原生执行服务，申请观察或控制会明确返回后端不支持。普通 none 配置直接使用当前完整快照，不裁剪字段适配旧节点。

## 校验取舍

移除了 ID 的字符格式和任意长度上限、坐标的固定数值上限、按键名称格式和五键限制、文本固定长度上限，以及将截图尺寸或等待时间偏好直接判为非法的固定上限。三击选择也在动作契约中允许。

必须保留：权限与 owner、停止和撤销、动作的必需字段、非有限或负坐标、实际观察图像范围、同 operation_id 去重、有限输入片段及传输字节预算。实际图像范围由执行服务根据 observation 检查；不能用某个固定屏幕分辨率代替。截图尺寸和等待时间由后端按能力处理并报告实际值，不能静默裁断图片或重发输入。

正式执行时校验目标应用窗口，不要求 AgentDock 面板在前台。目标失焦或几何变化应重新观察；内容标题、工具链年份、无关元数据格式不作为拒绝执行的理由。

## 验证与下一步

本轮使用 `MCP/go.work` 关联三个本地仓库进行构建；独立发布前还需发布 protocol 模块并同步依赖版本。

已通过 protocol 全包测试、AgentDock 受影响包及 race 测试、Nexus 项目／数据库／HTTP／节点包测试、HTTP 契约检查和前端构建。针对性测试覆盖三档权限、Full Access、当前 wire 字段保存、历史 Target 与跨 owner 拒绝、宽松 ID／按键／尺寸参数，以及非法动作拒绝。

后续按可运行闭环交付：用户会话内原生 helper 和本机 IPC；Go 服务装配与标准 MCP image 输出；会话控制、持久动作结果和取消；最后将预编译 helper 纳入安装包。上线工具前必须补齐这条执行链，不能把 P0 的自身校准点击直接包装成通用桌面能力。
