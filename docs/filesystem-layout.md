# 配置与文件路径

本分支的服务运行配置统一使用 TOML。下面是新安装和本轮服务模板使用的路径约定；旧预览包不会因文档更新自动迁移。构建清单、数据库记录、HTTP JSON 和 JSON Schema 是各自的工具或数据契约，不属于运行配置。

| 内容 | 核心 | 扩展 |
| --- | --- | --- |
| 程序 | `/usr/bin/lmm-core` | `/usr/bin/lmm-extensions` |
| 默认配置 | `/etc/lmm.conf.d/core.toml` | `/etc/lmm.conf.d/extensions.toml` |
| 私密文件 | `/etc/lmm.conf.d/secrets/core/` | `/etc/lmm.conf.d/secrets/extensions/` |
| 持久状态 | `/var/lib/lmm/core/` | `/var/lib/lmm/extensions/` |
| socket、PID 等临时状态 | `/run/lmm/core/` | `/run/lmm/extensions/` |
| 可选文件日志 | `/var/log/lmm/core/` | `/var/log/lmm/extensions/` |
| 默认日志 | systemd journal；OpenRC 由操作者配置日志收集 | systemd journal；OpenRC 由操作者配置日志收集 |

前端只读文件位于 `/usr/share/lmm/web/`，协议示例和模板位于 `/usr/share/lmm/legal/`。这些文件由软件包管理，不存运行状态、密钥、上传内容或数据库备份。协议站点配置使用 `/etc/lmm.conf.d/legal.toml`；操作者须显式指定模板目录，协议发布仍需独立审核。

`/run` 可以在重启后清空，持久业务记录不能存到这里。PostgreSQL 数据目录由数据库自身管理；`/var/lib/lmm/core` 并不意味着核心可以读取数据库服务的物理文件。扩展不得取得核心数据库的凭据或目录权限。路径约定也不表示每个进程已实现文件日志、PID 文件或本地缓存；仅启用实际实现的功能。

## 固定配置文件

每个服务只读取自己的固定默认文件，不扫描、排序或合并 `*.toml`。可用 `--config` 指定一个绝对路径；相对路径、未知键、重复键、格式错误和启用功能缺少必需字段都须在监听前拒绝。TOML 是数据，不能用 `source`、`.` 或 shell `eval` 载入。

```sh
/usr/bin/lmm-core --config /etc/lmm.conf.d/core.toml
/usr/bin/lmm-extensions --config /etc/lmm.conf.d/extensions.toml
```

核心允许 `--config` 位于子命令前后，例如 `lmm-core --config /absolute/private/owner.toml init-db`。初始化配置必须使用独立安装 owner，运行配置必须使用受限 runtime；不能把 owner 配置交给网络服务。`--help` 和 `--version` 不要求读取服务配置。

新服务模板和新部署流程只使用文件配置。两个服务的显式 `--legacy-env` 都是旧测试和受控迁移的临时兼容入口，与 `--config` 互斥；默认文件缺失也不能自动退回环境变量。文件模式拒绝旧运行时环境变量混入。兼容入口不改变规范路径，也不应写进新安装步骤。工具链变量、操作系统变量与构建用变量仍遵守各自契约。

主配置和功能配置引用均使用 TOML；连接串、口令和密钥通过绝对路径引用独立私密文件。文件模式不允许环境变量混合覆盖，不展开 `$VARIABLE`、`%d` 或任意 shell 内容。示例见 [core.example.toml](../config/core.example.toml) 和 [extensions.example.toml](../config/extensions.example.toml)。示例不含可用凭据，配置解析测试也不能证明数据库和业务已验收。

## 权限与服务账户

非敏感 TOML 可由 root 持有、权限 `0644`；限制可见范围时可用 `0640` 或 `0600`，但服务必须能够读取。配置文件不得允许组或其他用户写入。私密文件使用 root 或对应服务账户持有的 `0600`，或服务管理器提供的只读 `0400` 凭据文件；不得放进仓库、软件包、命令行参数或日志。私密目录须限制访问，核心与扩展分别授权。

当前 Linux 模板使用独立的静态 `lmm-core`、`lmm-extensions` 账户。systemd 平台随包提供 `sysusers.d` 声明，由操作者显式创建账户；OpenRC 包仅创建自己的非登录账户。当前模板读取由服务账户持有的 `0600` 私密文件，不使用 `LoadCredential`。采用 `LoadCredential` 的单独部署须把 TOML 引用改为服务实际能读取的绝对凭据路径；解析器不会替换 `%d`，不能只加一条 unit 指令就假定原 `/etc` 路径可读。

持久和临时服务目录默认使用 `0700`，分别归对应服务账户。共享 RPC 必须显式启用：用本机 `lmm-rpc` 组的实际数字 GID 设置 `rpc.shared_group_id`，只将 IPC 目录设为 `0750`、socket 设为 `0660`，锁文件保持 `0600`。core 的主组须匹配该 GID，extensions 加入同一组；服务分别保留自己的 `0600` token 文件，不能给共享组读取秘密。systemd 的可选配置片段随包放在 `/usr/share/doc/<程序>/rpc.service.conf.example`，不会自动启用。普通独立核心不需要共享组或扩展。不同 UID 的真实 RPC 连通性须另行验收，不能只凭路径或组名宣布可用。

共享 IPC 不开放 `/var/lib/lmm/core` 或核心私密目录。默认日志进入 journal；启用文件日志前另行配置写入路径、轮转和权限，不在包内生成日志内容。

## 客户端个人目录

`lmm` 安装为 `/usr/bin/lmm`。个人路径约定为：

| 内容 | Linux 路径，括号内为变量未设置时的默认值 |
| --- | --- |
| 配置 | `$XDG_CONFIG_HOME/lmm/config.toml`（`~/.config/lmm/config.toml`） |
| 持久个人状态 | `$XDG_STATE_HOME/lmm/`（`~/.local/state/lmm/`） |
| 可重建缓存 | `$XDG_CACHE_HOME/lmm/`（`~/.cache/lmm/`） |

这是客户端子项目的路径规范；本轮不修改 `apps/cli` 子模块或宣称其所有存储已迁移。实际客户端凭据机制、已实现的命令和平台差异，以 [TokenNotIncluded/lmm](https://github.com/TokenNotIncluded/lmm#readme) 的固定版本为准。服务包不写入用户主目录，也不创建额外产品程序。

## 升级、卸载与备份

配置文件使用软件包的 `config|noreplace` 保留规则；升级后操作者检查包管理器生成的新配置候选，按字段合并，再明确重启目标服务。升级安装文件不表示运行进程已加载它们。

软件包不创建秘密，不初始化或迁移数据库，不自动启用或重启服务。卸载没有删除私密文件、持久数据、审计历史或外部数据库的清理钩子；这些内容也不属于软件包文件。已修改配置由对应包管理器按其保留或改名规则处理，操作者应提前保留独立副本。卸载前由操作者明确停止目标服务。旧 `/etc/lmm-core/service.env`、`/etc/lmm-extensions/service.env`、`/var/lib/lmm-core` 和 `/usr/share/lmm-web/dist` 属于旧预览包布局；本轮不自动移动或清理旧安装中的这些内容。旧服务必须按其固定版本停止，配置由操作者审查后转换，不能把旧 shell 文件直接当 TOML。

备份目录由操作者显式选定，并检查所有权、权限和恢复过程；不把备份打包进发布产物，也不在升级或卸载时删除。

新布局的实际构建、配置解析、进程和安装验证必须分别记录。参见 [Linux 预览包](../packaging/linux/README.md)、[独立核心](core-standalone.md) 和 [开发指南](development.md)。
