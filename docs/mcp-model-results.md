# MCP 模型结果精简契约

## 返回边界

内置工具的 `structuredContent` 是完整、可机器读取的业务结果；`content[].text` 是最多 256 个 Unicode 字符加省略号的摘要，不再复制完整 JSON。图片、音频、资源链接保持 MCP 原生 content 类型。此契约面向读取 `structuredContent` 的 Host；只读取文本块的旧客户端需要接入结构化结果，不能把摘要当作完整文件或命令输出。

两个独立发行仓库的 `internal/mcpresult` 采用相同策略与回归测试，并依赖已发布的 protocol v0.10.0，不使用本地 `replace`。策略限定在 MCP 边界：REST、内部 Runtime 对象、权限校验、Journal、Bridge command-outcome ACK 与恢复记录不因此删减。

## 已知工具的精简规则

普通已退出命令只返回 `exit_code` 和非空 stdout/stderr。运行中命令仍有 `status` 与 `session_id`；未知/中断状态也保留可恢复句柄。超时、真实截断、持久化错误、信号退出只在发生时返回。纯 `exit status N` 文本不再重复同一个退出码。

`read_file` 保留正文与解析后的路径。完整读取不重复编码、大小及完整文件行数；局部读取保留行范围，截断保留续读游标与原因。`list_dir` 目录项保留相对路径与类型，部分读取或截断时仍明确说明。`search_text` 不返回 engine/扫描计量；保留实际匹配位置、必要上下文和不完整标记。上下文窗口不跨越不连续的 ripgrep 匹配组；恰好达到 max_results 不视为发生截断。

`file_edit` 默认不回传已提交写入的完整 diff；dry_run 或显式 max_diff_bytes 仍提供预览。结构化结果在文件提交前检查可编码性，不能把日志计量失败变成已提交后的工具失败。此措施不声称文件系统提交与网络确认具有原子性。

Task、Skill、动态 MCP 管理和 ACP 不重复固定 action、状态目录及成功确认。Task checkpoint 规则只在工具描述中说明一次。`count` 只有等于已返回集合长度时才删除；索引统计、分页总数和不相等的计数保留。业务对象中的 false、0、null 不递归删除，取消请求的 accepted 状态不冒充已完成。

ACP 事件保留 events、status、next_seq，发生环形缓冲丢失时保留截断与恢复位置；正文和嵌套 update 不按字段名裁剪。浏览器错误正确标记 MCP isError，错误 code 不重复，空错误列表不返回；页面位置、截图和视口信息保留。

动态 MCP 的远端 content 仅放在顶层，远端私有 `_meta` 不嵌入模型正文。独立文本、注解、图片/音频/资源不能删除；只有与远端 structuredContent 完全相同、没有注解的 JSON 文本副本才去重。未识别第三方工具以及纯 content envelope 保持原有内容。

## Project Context 与 Host ACK

Project 返回保留 WorkSession/Target 操作句柄、完整适用的 AGENTS.md 原文和不重复的部署信息。重复路由标识、相同 permissions、校验字节数和内部 revision/hash 不占用模型正文。

Host 从结果 `_meta["io.nexusdock/project-context-delivery"]` 读取本次交付的精确 `work_session_id`、可选 `target_id`、`context_revision`，并按现有 ACK 输入协议回传。标识来自本次已构造的结果，不从可能已变化的状态重新读取。持久化交付状态和原始权限/版本校验不变。最终 MCP envelope（含摘要与私有 metadata）受 Project Context 大小上限约束。

## 升级与界面

Nexus 在 Fleet 合并时仅标准化已知模型输出 schema，接受的 provider hashes 仍来自原始节点契约。入参类型、必填约束、可见性和执行权限不放宽；输出瘦身不会让新旧节点滚动升级期间停止工作。

MCP Apps 在浏览器内补充展示需要的 action/数组计数，Task 进度直接由 steps 计算，动态工具界面从顶层 content 取附件。补充字段不再经过模型返回通道。普通网页构建不需要重新引入已退休的 API 或客户端生成目录。

## 回归与交付验证

AgentDock：`make check`、`go test -race ./...`。

NexusDock：`make ci`、`cd web && npm test`。`make contracts` 额外执行生成器中的 `x-mcp-model-result` 示例，逐一和实际 Go builder 比较，防止文档示例与实现漂移。

回归覆盖两层 envelope 往返、成功/错误语义、可选 isError、4 MiB 正文单份传输、真实 10792-byte file_edit fixture、动态多模态/注解、私有 ACK、游标与截断、不可变原始数据、投影后 schema、Fleet 新旧 provider、界面脚本以及不安全入参漂移拒绝。
