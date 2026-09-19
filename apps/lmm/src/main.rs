use clap::{Args, Parser, Subcommand};
use lmm::{
    catalog,
    discovery::{self, ProbeContext, TargetStatus},
    environment::{self, Environment},
    oauth,
    plan::{Intent, Preview},
};
use serde::Serialize;
use std::{
    env,
    io::{self, IsTerminal, Write},
    path::PathBuf,
    process::ExitCode,
    time::{SystemTime, UNIX_EPOCH},
};

#[derive(Parser)]
#[command(
    name = "lmm",
    version,
    about = "LMM 安装与接入工具（开发预览：环境检查与操作规划）"
)]
struct Cli {
    /// 输出结构化 JSON；不交互、不输出配置内容
    #[arg(long, global = true)]
    json: bool,
    /// 无人值守：缺少目标或授权时直接返回
    #[arg(long, global = true)]
    non_interactive: bool,
    #[command(subcommand)]
    command: Command,
}

#[derive(Subcommand)]
enum Command {
    /// 搜索软件目录；目录条目不代表已验证兼容
    Catalog { query: Option<String> },
    /// 规划完整接入；当前版本在未具备适配器时明确阻止写入
    Setup(SetupArgs),
    /// 安装软件；不要求 LMM 登录
    Install(OperationArgs),
    /// 通过浏览器登录，凭据保存在系统凭据库
    Login(LoginArgs),
    /// 退出 CLI；不撤销独立应用授权
    Logout(AuthArgs),
    /// 读取当前授权模型与计费信息，不发起模型调用或更改选择
    Models(AuthArgs),
    /// 只读检查环境、安装线索与各层验证状态
    Status(TargetArgs),
    /// 同步 LMM 信息，保留当前服务商与模型
    Sync(OperationArgs),
    /// 更新明确纳入管理的软件
    Update(OperationArgs),
    /// 只读诊断；--fix 仍受适配器能力与确认范围限制
    Doctor(DoctorArgs),
    /// 按字段恢复 LMM 变更，保留后续修改
    Restore(OperationArgs),
    /// 解除指定软件接入并撤销对应授权
    Disconnect(RequiredTarget),
    /// 卸载已确认纳入管理的软件，默认保留数据
    Uninstall(RequiredTarget),
    /// 按当前配置启动指定软件
    Run(RequiredTarget),
}

#[derive(Args)]
struct AuthArgs {
    /// 信任的 LMM HTTPS origin（自托管时显式指定）
    #[arg(long, default_value = oauth::DEFAULT_ISSUER)]
    issuer: String,
}

#[derive(Args)]
struct LoginArgs {
    #[command(flatten)]
    auth: AuthArgs,
    /// 只显示登录链接；仍需要浏览器能访问当前机器的回环端口
    #[arg(long)]
    no_browser: bool,
    /// 浏览器回调等待上限（秒）
    #[arg(long, default_value_t = 180, value_parser = clap::value_parser!(u64).range(10..=600))]
    timeout: u64,
}

#[derive(Args)]
struct TargetArgs {
    /// 精确的软件目录 ID，例如 astrbot 或 cc-switch
    software: Option<String>,
    /// 明确的本地实例根目录；不自动扫描远程或 Docker 实例
    #[arg(long, requires = "software")]
    instance: Option<PathBuf>,
}

#[derive(Args)]
struct OperationArgs {
    #[command(flatten)]
    target: TargetArgs,
    /// 仅包含当前环境中已安装且受支持的目标；更新还要求管理授权
    #[arg(long, conflicts_with_all = ["software", "instance"])]
    all: bool,
    /// 只生成计划，不执行修改
    #[arg(long)]
    dry_run: bool,
    /// 确认已展示范围；不代表允许切换账号、服务商或付费测试
    #[arg(long)]
    yes: bool,
}

#[derive(Args)]
struct SetupArgs {
    #[command(flatten)]
    operation: OperationArgs,
    /// 仅添加 LMM，保留当前选择
    #[arg(long, conflicts_with = "activate")]
    add_only: bool,
    /// 明确请求添加并切换到 LMM（不表示已执行）
    #[arg(long)]
    activate: bool,
}

#[derive(Args)]
struct DoctorArgs {
    #[command(flatten)]
    target: TargetArgs,
    /// 请求修复已获允许的 LMM 配置；不会绕过能力检查
    #[arg(long)]
    fix: bool,
    /// 生成可主动分享的摘要，去除路径和配置内容
    #[arg(long)]
    report: bool,
}

#[derive(Args)]
struct RequiredTarget {
    software: String,
    #[arg(long)]
    instance: Option<PathBuf>,
}

#[derive(Serialize)]
struct Report {
    schema_version: u32,
    command: &'static str,
    outcome: &'static str,
    checked_at: u64,
    check_scope: &'static str,
    environment: Environment,
    targets: Vec<TargetStatus>,
    plans: Vec<Preview>,
    notes: Vec<&'static str>,
}

impl Report {
    fn new(command: &'static str, targets: Vec<TargetStatus>) -> Self {
        Self {
            schema_version: 1,
            command,
            outcome: "inspected",
            checked_at: SystemTime::now()
                .duration_since(UNIX_EPOCH)
                .map_or(0, |duration| duration.as_secs()),
            check_scope: "local_path_evidence_only",
            environment: Environment::detect(),
            targets,
            plans: vec![],
            notes: vec![],
        }
    }

    fn redact(&mut self) {
        for target in &mut self.targets {
            target.instance = target
                .instance
                .as_ref()
                .map(|_| PathBuf::from("[redacted]"));
            for evidence in &mut target.evidence {
                evidence.path = PathBuf::from("[redacted]");
            }
        }
    }
}

fn context() -> ProbeContext {
    ProbeContext {
        home: environment::user_home(),
        path: env::var_os("PATH")
            .map(|value| env::split_paths(&value).collect())
            .unwrap_or_default(),
    }
}

fn inspect(target: &TargetArgs, context: &ProbeContext) -> Result<Vec<TargetStatus>, String> {
    if target
        .instance
        .as_ref()
        .is_some_and(|path| !path.is_absolute())
    {
        return Err("--instance 必须是当前环境的绝对本地路径；远程与容器适配器尚未提供".into());
    }
    match target.software.as_deref() {
        Some(id) => {
            let software = catalog::find(id)
                .ok_or_else(|| "未知软件 ID；运行 lmm catalog 查看精确名称".to_owned())?;
            if target.instance.is_some() && !matches!(id, "astrbot" | "cc-switch") {
                return Err("此软件尚未实现实例选择，不能忽略 --instance 后继续操作".into());
            }
            Ok(vec![discovery::discover(
                software,
                context,
                target.instance.as_deref(),
            )])
        }
        None => Ok(catalog::SOFTWARE
            .iter()
            .map(|software| discovery::discover(software, context, None))
            .collect()),
    }
}

fn select_software() -> Result<String, String> {
    let mut output = io::stderr().lock();
    writeln!(output, "选择要规划接入的软件（当前均未完成集成验证）：")
        .map_err(|_| "无法显示选择菜单")?;
    for (i, software) in catalog::SOFTWARE.iter().enumerate() {
        writeln!(output, "  {}. {} ({})", i + 1, software.name, software.id)
            .map_err(|_| "无法显示选择菜单")?;
    }
    write!(output, "输入序号，或直接回车取消：").map_err(|_| "无法显示选择菜单")?;
    output.flush().map_err(|_| "无法显示选择菜单")?;
    let mut input = String::new();
    io::stdin()
        .read_line(&mut input)
        .map_err(|_| "无法读取选择")?;
    let index = input
        .trim()
        .parse::<usize>()
        .ok()
        .and_then(|index| index.checked_sub(1));
    index
        .and_then(|index| catalog::SOFTWARE.get(index))
        .map(|software| software.id.to_owned())
        .ok_or_else(|| "未选择目标；没有执行任何修改".to_owned())
}

fn operation_report(
    name: &'static str,
    args: &OperationArgs,
    intent: Intent,
    interactive: bool,
    context: &ProbeContext,
) -> Result<Report, String> {
    let selected;
    let target = if args.target.software.is_none() && !args.all {
        if !interactive {
            return Err("无人值守模式需要明确软件或 --all；没有执行任何修改".into());
        }
        selected = TargetArgs {
            software: Some(select_software()?),
            instance: None,
        };
        &selected
    } else {
        &args.target
    };
    let mut targets = inspect(target, context)?;
    // Qualified installation and adapter evidence are prerequisites for --all.
    // No adapter is qualified in this milestone; PATH hits cannot bypass this gate.
    if args.all {
        targets.clear();
    }
    let mut report = Report::new(name, targets);
    report.outcome = "blocked";
    for target in &report.targets {
        let mut preview = Preview::blocked(target.software, intent);
        if name == "install" {
            preview.account = "not_required";
            preview.blockers = vec!["缺少经过验证的软件版本、下载来源校验及安装适配器"];
        } else if name == "restore" {
            preview.account = "not_required";
            preview.blockers = vec!["尚未接入目标的变更日志与原生事务适配器，不能直接覆盖备份"];
        }
        report.plans.push(preview);
    }
    if args.all {
        report.notes.push(
            "当前版本没有经过验证的自动接入适配器，--all 没有可执行目标；未安装或修改任何软件",
        );
    }
    if args.dry_run {
        report
            .notes
            .push("仅规划；阻塞项仍需解决，不能作为接入完成的证据");
    }
    if args.yes {
        report.notes.push("--yes 不会跳过缺失能力、授权或费用限制");
    }
    if name == "install" {
        report
            .notes
            .push("安装本身不需要 LMM 登录；当前缺少经过验证的安装适配器");
    }
    Ok(report)
}

fn write_json(value: &impl Serialize) -> Result<(), String> {
    let mut output = io::stdout().lock();
    serde_json::to_writer_pretty(&mut output, value).map_err(|_| "无法输出 JSON".to_owned())?;
    writeln!(output).map_err(|_| "无法输出 JSON".to_owned())
}

fn emit(report: &Report, json: bool) -> Result<(), String> {
    if json {
        return write_json(report);
    }
    let mut output = io::stdout().lock();
    writeln!(
        output,
        "LMM · {} · {} / {}",
        report.command, report.environment.os, report.environment.arch
    )
    .map_err(|_| "无法输出结果")?;
    writeln!(
        output,
        "范围：当前用户、当前运行环境；检查时间：{}（Unix 秒）",
        report.checked_at
    )
    .map_err(|_| "无法输出结果")?;
    for target in &report.targets {
        writeln!(
            output,
            "\n{}：安装状态无法确认；安装管理者、配置管理者均未知",
            target.software
        )
        .map_err(|_| "无法输出结果")?;
        for evidence in &target.evidence {
            // Debug formatting escapes control characters in user-controlled paths.
            writeln!(
                output,
                "  {:?}: {:?}（仅路径线索）",
                evidence.path, evidence.observation
            )
            .map_err(|_| "无法输出结果")?;
        }
        writeln!(
            output,
            "  LMM 添加/启用：未知；授权、连通性、目标调用、流式及工具调用：未检查"
        )
        .map_err(|_| "无法输出结果")?;
    }
    for plan in &report.plans {
        writeln!(
            output,
            "\n{} 计划已阻止：无配置修改、无付费调用、无重启",
            plan.software
        )
        .map_err(|_| "无法输出结果")?;
        for blocker in &plan.blockers {
            writeln!(output, "  - {blocker}").map_err(|_| "无法输出结果")?;
        }
    }
    for note in &report.notes {
        writeln!(output, "\n{note}").map_err(|_| "无法输出结果")?;
    }
    Ok(())
}

fn execute(cli: &Cli) -> Result<u8, String> {
    if cli.non_interactive
        && matches!(
            &cli.command,
            Command::Login(_) | Command::Logout(_) | Command::Models(_)
        )
    {
        return Err("当前系统凭据库可能需要解锁交互；OAuth 命令尚不支持 --non-interactive，未访问凭据或网络".into());
    }
    let context = context();
    let interactive = !cli.json
        && !cli.non_interactive
        && io::stdin().is_terminal()
        && io::stderr().is_terminal();
    let report = match &cli.command {
        Command::Catalog { query } => {
            let entries = catalog::search(query.as_deref().unwrap_or_default());
            if cli.json {
                write_json(&entries)?;
            } else {
                let mut output = io::stdout().lock();
                for software in entries {
                    writeln!(output, "{} — {}\n  {}\n  来源：{}\n  安装/接入：尚未支持；维护：仅本地线索检查；兼容版本/平台/场景：未验证\n", software.id, software.name, software.purpose, software.source).map_err(|_| "无法输出目录")?;
                }
            }
            return Ok(0);
        }
        Command::Status(target) => Report::new("status", inspect(target, &context)?),
        Command::Doctor(args) => {
            let mut report = Report::new("doctor", inspect(&args.target, &context)?);
            report.outcome = "incomplete";
            report.notes.push("诊断未完成：仅检查本地路径线索；请核对目标实例、版本及配置管理关系。没有执行网络或付费测试");
            if args.fix {
                report.outcome = "blocked";
                report
                    .notes
                    .push("当前没有经过验证的修复适配器；未修改配置");
            }
            if args.report {
                report.redact();
            }
            report
        }
        Command::Setup(args) => operation_report(
            "setup",
            &args.operation,
            if args.activate {
                Intent::Activate
            } else if args.add_only {
                Intent::AddOnly
            } else {
                Intent::Default
            },
            interactive,
            &context,
        )?,
        Command::Install(args) => {
            operation_report("install", args, Intent::AddOnly, interactive, &context)?
        }
        Command::Sync(args) => {
            operation_report("sync", args, Intent::Maintain, interactive, &context)?
        }
        Command::Update(args) => {
            operation_report("update", args, Intent::Maintain, interactive, &context)?
        }
        Command::Restore(args) => {
            operation_report("restore", args, Intent::Maintain, interactive, &context)?
        }
        Command::Login(args) => {
            if !interactive {
                return Err("登录需要浏览器授权；请在交互终端运行 lmm login。无人值守模式不会等待或代替用户同意".into());
            }
            let result = oauth::login(
                &args.auth.issuer,
                std::time::Duration::from_secs(args.timeout),
                |url| {
                    let mut output = io::stderr().lock();
                    writeln!(
                        output,
                        "请在浏览器确认 LMM CLI 授权（仅目录和余额读取，不迁移任何应用）：\n{url}"
                    )
                    .map_err(|_| oauth::AuthError::Output)?;
                    if !args.no_browser && open::that_detached(url).is_err() {
                        writeln!(output, "浏览器未能自动打开，请在本机浏览器打开上述链接。")
                            .map_err(|_| oauth::AuthError::Output)?;
                    }
                    Ok(())
                },
            );
            return match result {
                Ok(status) => {
                    if cli.json {
                        write_json(&status)?;
                    } else {
                        writeln!(io::stdout().lock(), "LMM CLI 已登录，凭据已保存到系统凭据库。尚未授予应用调用权限；可运行 lmm models。")
                            .map_err(|_| "无法输出登录结果")?;
                    }
                    Ok(0)
                }
                Err(error) => auth_failure(error, cli.json),
            };
        }
        Command::Logout(args) => {
            return match oauth::logout(&args.issuer) {
                Ok(outcome) => {
                    if cli.json {
                        write_json(
                            &serde_json::json!({"outcome": outcome, "applications_changed": false, "other_application_grants": "not_checked"}),
                        )?;
                    } else {
                        writeln!(
                            io::stdout().lock(),
                            "CLI 已退出；其他应用授权未撤销，当前版本尚不能列出这些授权。"
                        )
                        .map_err(|_| "无法输出退出结果")?;
                    }
                    Ok(0)
                }
                Err(error) => auth_failure(error, cli.json),
            };
        }
        Command::Models(args) => {
            return match oauth::models(&args.issuer) {
                Ok(catalog) => {
                    if cli.json {
                        write_json(&catalog)?;
                    } else {
                        let mut output = io::stdout().lock();
                        writeln!(
                            output,
                            "当前授权模型（不代表目标软件兼容；价格可能随使用变化）："
                        )
                        .map_err(|_| "无法输出模型")?;
                        for model in &catalog.models {
                            writeln!(output, "{:?} · {:?} · {:?}\n  价格单位：{:?} / {:?}；输入：{:?}，输出：{:?}，每次请求：{:?}；依据：{:?}\n  null/None 表示未知；未调用或切换模型。", model.id, model.upstream_model, model.group, model.pricing.currency, model.pricing.unit, model.pricing.input, model.pricing.output, model.pricing.request, model.pricing.price_basis).map_err(|_| "无法输出模型")?;
                        }
                    }
                    Ok(0)
                }
                Err(error) => auth_failure(error, cli.json),
            };
        }
        Command::Disconnect(args) | Command::Uninstall(args) | Command::Run(args) => {
            let name = match &cli.command {
                Command::Disconnect(_) => "disconnect",
                Command::Uninstall(_) => "uninstall",
                _ => "run",
            };
            let target = TargetArgs {
                software: Some(args.software.clone()),
                instance: args.instance.clone(),
            };
            let mut report = Report::new(name, inspect(&target, &context)?);
            report.outcome = "blocked";
            report
                .notes
                .push("未实现该目标的安全执行适配器；未启动或卸载软件，未修改配置或撤销授权");
            report
        }
    };
    emit(&report, cli.json)?;
    Ok(if matches!(report.outcome, "blocked" | "incomplete") {
        3
    } else {
        0
    })
}

fn auth_failure(error: oauth::AuthError, json: bool) -> Result<u8, String> {
    if json {
        write_json(
            &serde_json::json!({"schema_version":1,"outcome":"blocked","error":error.to_string()}),
        )?;
    } else {
        eprintln!("lmm: {error}");
    }
    Ok(3)
}

fn main() -> ExitCode {
    let cli = Cli::parse();
    match execute(&cli) {
        Ok(code) => ExitCode::from(code),
        Err(error) => {
            if cli.json {
                let _ = write_json(
                    &serde_json::json!({"schema_version": 1, "outcome": "invalid_request", "error": error}),
                );
            } else {
                eprintln!("lmm: {error}");
            }
            ExitCode::from(2)
        }
    }
}
