# 任务复核、返工与证据关联

`task_manage` 在 `create`、`get`、`resume` 成功返回中交付当前 `checkpoint_policy`。`prompt` 是 checkpoint 的保存时机、摘要内容与交接提示词；`rules` 是不可由提示词修改的参数及状态约束。来源和版本在 `source` / `version` 中提供，`enforcement=caller_driven` 表示调用方主动保存。MCP 及 Nexus 转发均保留该策略。

NexusDock 控制台的“任务 → Checkpoint 提示词”支持编辑与恢复默认，配置统一适用于已连接节点。AgentDock 任务服务通过 Device Token 只读获取 `/v1/settings/checkpoint`，下一次创建、读取或恢复任务时交付最新版，不依赖项目 AGENTS.md 或 MCP 设置。独立运行使用内置提示词；Nexus 不可用时显式返回 `warning` 并交付内置提示词，仍允许恢复本地任务。管理端写入必须携带 `expected_revision`，过期编辑会被拒绝。配置修改不改变 `task_id`、`summary` 必填项或单步/批量字段互斥约束。

未预先拆步骤或无需改变步骤状态时，可以记录任务级 checkpoint：

```json
{"action":"checkpoint","task_id":"tsk_...","summary":"已定位失败原因，日志见 command-result:123；下一步修复并复测"}
```

此模式只更新摘要和 checkpoint 事件，保留步骤、阶段和任务状态。指定 `step_id` 时仍必须提供合法 `status`；批量字段仍要求有效步骤，不能与单步字段混用。空摘要和超长摘要会被拒绝。blocked 任务必须先 resume；终审已通过或任务已完成时不能 checkpoint。失败终审后的新 checkpoint 会将旧终审及证据移入历史，后续仍须重新终审。紧邻的相同摘要 checkpoint 不重复追加事件。

checkpoint 仍由调用方主动提交。服务端验证输入、状态约束并持久化，不自动生成摘要或定时保存，也不把最终一次批量更新认定为已满足某种中间保存频率。

完成条件只在去除首尾空白后做大小写敏感的精确去重。平台、否定词、数字、大小写和补充要求均保留；原始数组及文本先检查资源上限。条件 ID 在创建时生成，返工不改变 ID。旧版本已经删除并持久化的条件无法从现有任务文件自动还原，需要按原始需求核对。

`task_manage` 新增显式 `reopen`：

```json
{"action":"reopen","task_id":"tsk_...","step_id":"test","reopen_reason":"复核发现 Windows 路径处理仍有错误"}
```

仅 active 任务中的 completed 步骤可以重开，并继续遵守单个 in_progress 步骤约束。步骤进入 in_progress，revision 递增；完整原因写入 step_revisions。当前 final_review（包含条件证据）保留在 review_history 并从有效终审中移除，同时清理候选。返工后必须重新完成步骤及终审才能 complete。归档后的任务保持不可变。普通 checkpoint 仍只允许向前推进；failed review 后推进也保留旧终审历史。

终审可按完成条件 ID 保存证据定位：

```json
{
  "action":"final_review",
  "task_id":"tsk_...",
  "status":"pass",
  "summary":"完成本轮检查",
  "evidence":[
    {"condition_id":"cond_01","summary":"Windows 回归测试通过","evidence_ref":"command-result:...","source":"self_reported"}
  ]
}
```

`evidence_ref` 可以指向已保存的命令结果、artifact 或观察快照。运行时检查条件 ID 和资源边界，但不会读取或验真该引用。调用方证据统一为 self_reported，拒绝调用方自行标记 machine_verified。现有 verified 文本同样是自报；pass 不代表机器验真，也不自动声称每个条件都有证据。摘要返回 evidence_count 与 uncovered_condition_ids，完整证据和失效历史通过 get 获取。可信机器验证器的结果摄取不在此接口中实现。

重开使本地旧 review_revision 不再能用于新的证据提交；已投递到 Evolution 的历史学习记录不会被追溯删除，仍受该系统同一任务不得重复投票的约束。

# MCP 与续跑修复边界

MCP 服务队列响应调用 context；配置 timeout 从请求入口计算，包含注册表与服务队列等待。排队后以状态引用复验配置代次。旧连接在注册表锁外回收，Close 等待当前连接及所有待回收连接。

连接关闭后在下一次显式请求建立新 session；正在执行时断线返回 MCP_EXECUTION_UNKNOWN、retryable=false，禁止自动重放。业务错误、明确拒绝与普通超时不一概触发重连。成功发现空工具列表会缓存，工具目录变化可使用显式 refresh。

Nexus continuation 的 status/state 在无过期转换时只读 SELECT；需要过期转换时重新进入写事务并重读。MCP App 无 wake 时不 acquire，空闲轮询从 2 秒逐步退避到最多 30 秒；新工作出现后恢复基础间隔。心跳仍保留租约续期，最长轮询间隔低于 90 秒租约。
