# lmm

LMM 生态的 Rust CLI 子项目，包名与可执行文件名均为 `lmm`。

**当前是开发预览，已实现只读发现、桌面 OAuth 登录和模型目录读取，不是可完成应用接入的产品版本。**
`setup`、安装、同步、更新、恢复、解除接入、卸载和启动目前会明确返回阻塞原因，
不会以空操作返回成功。没有修改第三方软件或生产服务。

完整产品要求见 [原始需求](docs/requirements.zh-CN.md)，逐阶段交付见
[实施与验收计划](docs/implementation.md)。完整需求保留为目标，不因当前实现范围而缩减。

## 当前可用

- `lmm catalog [关键词]`：搜索明确项目身份的软件目录，分别说明安装、接入、维护能力。
- `lmm status [软件]`：当前进程 OS/架构、WSL/容器/SSH 线索、PATH 候选文件及显式实例路径检查。
- `lmm doctor [软件] --report`：只读诊断摘要，移除实例和文件路径，不读取配置内容。
- `lmm setup [软件] --dry-run`：生成带阻塞原因的预览；无目标且有终端时可选择软件。
- `--json`：结构化结果；自动禁用交互。`--non-interactive` 同样禁止等待输入。
- `lmm login`：桌面浏览器 PKCE 登录；凭据保存于系统凭据库。
- `lmm models [--json]`：获取当前授权模型及价格，不调用模型、不切换配置。
- `lmm logout`：撤销当前 issuer 下的 CLI 令牌族，成功后删除本地凭据；不影响 Pi/DSH 等其他应用。

## 登录与模型目录

```bash
lmm login
lmm models --json
lmm logout
# 自托管必须明确指定可信的 HTTPS origin，三个命令使用同一 issuer
lmm login --issuer https://lmm.example.com --no-browser
```

服务端需要包含本次 `lmm` 客户端注册改动并启用已有 OAuth 服务。
**代码提交不等于生产服务已部署；本次没有部署服务器，也没有完成真实账号浏览器验收。**
CLI 只申请 `catalog:read balance:read` 和同意页显示的分组快照；没有 `models:invoke`、
MCP 或账号管理权限。CLI 登录成功不代表 AstrBot 或 CC Switch 已授权。

浏览器回调只监听 `127.0.0.1` 随机端口，等待默认 180 秒，可用 `--timeout 10..600` 调整。
`--no-browser` 仅关闭自动打开浏览器，**不是设备码登录**：浏览器仍须能访问本机回环端口。
无图形服务器跨设备授权尚未实现；非交互 `login` 直接返回，不暗中等待授权。
切换账号先 `logout` 再 `login`；已有 CLI 登录不会被新登录静默覆盖。

Windows 使用 Credential Manager，macOS 使用 Keychain，Linux 使用 Secret Service。
凭据库可能显示系统解锁提示；不可用时明确报错，不使用明文文件后备。
因此认证命令暂不支持 `--non-interactive`，会在访问凭据库之前退出；`models --json` 只禁用 CLI 文本交互，系统仍可能要求解锁。
源码构建将 Linux D-Bus 库静态编入，
运行时仍需要可用的 Secret Service；不能将开发机单测当作三个系统的实际凭据库验收。

认证操作使用当前用户状态目录中的无密钥 `oauth.lock`，防止并发登录/刷新/退出。
Linux 为 `$XDG_STATE_HOME/lmm` 或 `~/.local/state/lmm`，macOS 为
`~/Library/Application Support/lmm`，Windows 为 `%LOCALAPPDATA%/lmm`。
Unix 目录/锁文件新建权限分别为 0700/0600；已有不安全目录会被拒绝。
刷新前将“不可重试”状态存入凭据库，响应丢失或保存失败后不会自动重放旧 refresh token。
此时需要退出再登录；退出撤销失败保留本地记录供重试，不能报告已完成。

模型目录只表示账号可见范围，不表示某个软件已经兼容。未知价格保留 null，
价格包含服务端返回的倍率，不重复计算；本命令不执行付费测试。

## 本地软件检查

显式实例目前只接受 AstrBot 部署根目录、CC Switch 配置根目录：

```bash
lmm status astrbot --instance /absolute/path/to/AstrBot
lmm status cc-switch --instance /absolute/path/to/.cc-switch
lmm doctor astrbot --instance /absolute/path/to/AstrBot --report --json
lmm setup astrbot --dry-run --json
```

Windows 使用当前 Windows 环境的绝对路径。AstrBot 只检查所选根目录的
`data/cmd_config.json` 路径；CC Switch 检查 `cc-switch.db` 路径，不打开数据库。
不自动搜索项目、其他用户、Docker 实例、局域网或远程机器。
PATH 文件可能是包装脚本或来自其他环境，因此只标为线索；不执行 `--version`。
符号链接和不可访问项显示 `unknown`，文件缺失只描述被检查的那个位置。

目前所有软件目录条目的兼容版本/平台/场景列表为空，均未验证。
配置管理者、授权状态、实际服务商、模型及真实调用路径不会凭路径推断。
`status` 的退出码 0 只表示完成线索查询，不能用于判断“软件可使用”。
`doctor` 尚不能完成语义检查，会返回 3。`status`、`doctor`、`setup --dry-run` 没有网络调用或付费验证。

`--all` 不把目录视为安装列表；当前没有合格适配器，因此返回空计划与退出码 3。
`--yes` 不能消除能力缺失，不能代表支付、重新授权或切换服务商的同意。
单独 `install` 的计划不要求登录。

## 开发

```bash
cargo run --manifest-path apps/lmm/Cargo.toml --locked -- catalog
cargo test --manifest-path apps/lmm/Cargo.toml --locked
cargo clippy --manifest-path apps/lmm/Cargo.toml --locked --all-targets --all-features -- -D warnings
cargo fmt --manifest-path apps/lmm/Cargo.toml --all --check
cargo build --manifest-path apps/lmm/Cargo.toml --locked --release
```

也可从仓库根目录使用 `just lmm catalog`、`just test-lmm`、`just build-lmm`。
独立 Cargo 工程与后端 `apps/api-rust` 分开锁定依赖，避免将 CLI 发布绑定到后端构建。
源码开发需要 Rust；面向普通用户的签名二进制、安装器和更新渠道尚待发布，
当前没有声称普通用户已经能“一键安装”。CI 构建产物仅作开发预览，不自动发布。

## 退出码与安全边界

| 退出码 | 含义 |
| --- | --- |
| 0 | 目录/只读查询已输出；请继续检查各层状态 |
| 2 | 参数不完整、目标未知、无法选择目标或输出失败 |
| 3 | 能力未实现、检查未完成或操作被阻止 |

`restore_fields` 是内存中的三方字段恢复算法：仅在字段仍等于 LMM 写入值时撤销，
保留用户后续修改；任一冲突使整个目标的计算失败。它拒绝整对象、数组及数组索引恢复。
它没有连接文件写入、日志、授权或命令执行，不能当作 `lmm restore` 已完成的证据。
同样，付费检查函数只是客户端规划规则，**不能代替服务端消费限制**。

指纹函数只用于有界配置快照的内容比较，不是原子写锁。未来接入必须使用目标软件原生
事务/管理接口解决并发写入；“比较哈希后覆盖文件”不满足完整并发修改验收。

只读环境查询不建立状态目录；OAuth 操作会创建上述锁目录并使用系统凭据库。
LMM 不复制第三方凭据、不上传遥测、不进行付费调用。
分享前建议使用 `doctor --report --json`；普通 `status` 会显示本地路径。
