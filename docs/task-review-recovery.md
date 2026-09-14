# 任务复核、返工与证据关联

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
