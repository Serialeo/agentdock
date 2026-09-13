# Linux 配置与生效

适用于官方 Linux 安装脚本创建的 systemd/OpenRC 服务，以及用户自己用服务管理器托管 AgentDock 的场景。

## 配置事实源

官方 Linux 安装脚本允许用户选择：

- 环境文件路径；
- 服务名；
- 服务用户（默认为执行安装的用户，sudo 安装时使用 `SUDO_USER`）；
- systemd / OpenRC / 不安装系统服务。

默认环境文件是：

```text
/etc/agentdock/agentdock.env
```

默认服务名是：

```text
agentdock
```

这些都可以被安装参数/环境变量覆盖，所以修改前先检查真实 service 定义：

```bash
systemctl cat agentdock
```

重点确认 `User=`、`Group=`、`EnvironmentFile=`、`ExecStart=` 和实际 service 名称。OpenRC 则检查实际 `/etc/init.d/<service>` 中的 `command_user` 和环境文件。

安装器不再创建 `agentdock` 系统账号。可通过 `AGENTDOCK_SERVICE_USER` 指定其他已存在的用户；不存在的用户会在部署前报错。直接以 root 安装且没有 `SUDO_USER` 时，默认运行用户为 root。

`HOME` 和默认数据根目录均使用运行用户在系统账号记录中的真实主目录；不要把 sudo 后的 root home 当成安装者的 home。默认直接创建 `~/.agentdock`（状态、Skill、会话）和 `~/AgentDock`（默认工作目录），不再使用 `/srv/agentdock`。安装器在环境文件中显式设置 `HOME`、`AGENTDOCK_HOME` 和 `AGENTDOCK_DEFAULT_DIR`，保证服务和核心 Skill 初始化使用同一份数据。

`AGENTDOCK_DATA_DIR` 可指定其他数据根目录，此时仅将两个 AgentDock 目录放在该根目录下，`HOME` 仍是运行用户的真实主目录。安装器只递归调整 `.agentdock` 和 `AgentDock` 两个受管目录的所有者，不修改整个用户主目录的所有权。

本安装方案仅面向当前目录布局的全新安装，不提供旧安装兼容或数据迁移流程。

安装程序默认仍在 `/opt/agentdock`（从源码目录启动安装时可沿用该源码目录），服务环境文件默认仍在 `/etc/agentdock/agentdock.env`；它们与用户数据目录是不同用途。

官方安装器还会在环境文件所在目录写 `desktop-runtime.json`，其中记录 service manager、service name、Core binary 和 environment file 等运行信息。它是运行清单，不是所有配置键的替代文件。

## 修改方式

systemd/OpenRC 部署时，修改服务实际加载的环境文件，而不是另开一个 shell `export` 后期待后台服务继承。

官方安装器最终将环境文件设为运行用户及其主组所有，权限为 `0600`，配置目录权限为 `0700`。修改时保持现有 owner/mode，不要把认证配置暴露给其他用户。

如果需要给 `exec_command` 透传宿主环境变量，必须同时满足：

1. 源变量真实存在于 AgentDock **服务进程**的环境中；
2. `AGENTDOCK_COMMAND_ENV_FROM_ENV_JSON` 显式声明子进程变量到宿主变量的映射。

服务不会自动读取某个登录用户的 `.bashrc`、`.zshrc` 或交互式 Shell 环境。

以安装用户运行后，AgentDock 的文件和命令操作具有该账号的 Linux 权限及附加组权限。若用户安装的命令找不到，检查服务实际的 `PATH`，按需在环境文件中配置绝对路径；不要仅为共享权限创建 `agentdock` 用户或放宽个人凭据权限。

## 卸载

卸载器默认同样按 `AGENTDOCK_SERVICE_USER`、`SUDO_USER`、当前用户的顺序确定账号和主目录。使用自定义数据根目录时，显式提供对应的 `AGENTDOCK_DATA_DIR`；服务名、安装目录和环境文件路径也要与真实安装一致。

`--services-only` 保留程序、配置和数据；`--purge-data` 会清理程序、配置以及数据根目录下的 `.agentdock`、`AgentDock`。即使数据根目录就是用户主目录，也只删除这两个受管子目录，保留主目录、其他个人文件和系统账号。

## 生效

systemd：

```bash
sudo systemctl restart <实际服务名>
sudo systemctl status <实际服务名> --no-pager
```

OpenRC：

```bash
sudo rc-service <实际服务名> restart
sudo rc-service <实际服务名> status
```

只修改 EnvironmentFile 的内容通常不需要 `daemon-reload`；如果同时修改了 systemd unit 本身，则先执行：

```bash
sudo systemctl daemon-reload
```

再重启服务。

如果安装时选择了“不安装系统服务”，则按直接运行二进制处理：重新加载真实启动环境并重启进程。

## 验证与排障

优先检查：

```bash
curl -fsS http://127.0.0.1:<实际端口>/healthz
```

systemd 日志：

```bash
sudo journalctl -u <实际服务名> -n 100 --no-pager
```

OpenRC Core 日志为 `/var/log/<service>/agentdock.err.log`；Tunnel 日志为 `/var/log/<tunnel-service>/cloudflared.out.log` 和 `cloudflared.err.log`，由 AgentDock 负责轮转。

如果“文件已经改了但行为没变”，重点确认：

1. 是否编辑了 service 真正引用的 EnvironmentFile；
2. 服务是否真的重启成功；
3. 新进程是否因为配置校验失败而反复退出；
4. 是否有 systemd unit 的额外 `Environment=`、容器层或外部进程管理器覆盖了预期值。
