# 离线生成维护计划

`scripts/build-credit-financial-plan.py` 只读本地输入，写新的 intent / runner plan。它不会联网、启动服务或连接数据库。输出是 canonical JSON、`O_EXCL`、`0600`；已有文件不会覆盖。seed 也必须是当前用户持有的 `0600` 常规文件。

从 `scripts/fixtures/credit-financial-plan-seed.example.json` 复制私有 seed，替换所有示例值和零 hash。此公开模板是字段样例，不能直接用于维护。先填最终已验签 provider 的本地路径 / SHA、最终 source commit、完整节点清单与数据库身份：`os_user` / `peer_role` 是备份和金融 SQL 的本地 peer 入口；数据库 owner 和运行服务 role 都是 `lmm_api`。克隆使用 `lmm_api`，不要把 `postgres` peer role 当成运行 role。

先生成 intent：

```sh
python3 scripts/build-credit-financial-plan.py intent \
  --seed /home/lightjunction/.cache/financial-private/seed.json \
  --output /home/lightjunction/.cache/financial-private/intent.json
```

使用这个 intent hash，先密封每个节点的 prepare config、两份 handoff 和 guardian unit，再启动该 unit。此时旧 writer 仍在运行，没有关闭 admission 或执行金融 SQL；guardian 先持有三把正常 owner 锁，正式 staging 才能借用这些锁。capture、prebridge、post 始终使用三个不同的正常 owner workspace；native prebridge 可按下文推迟到正式停止封存后 stage：

- `handoff` 是 `stage=prebridge` 的初始 handoff，供 capture 和 guardian 使用。
- `post_intent` 是正式 `stage=post` staging intent。它具有同一 immutable transition、provider、prepare config、guardian socket、URL 和探针 token 身份；此时不填假 previous owner、PID、capture receipt 或 shutdown journal。正式 post plan 绑定其真实 SHA。
- 已 stage 的 native owner 的 `staged_plan` 必须指向该 workspace 的 `staging/release-plan.json`，填写真实 SHA；`operator` 必须是已暂存的最终 provider 二进制。systemd workspace 位于 `/var/lib/lmm-api-deploy-systemd/RELEASE`，`migrate` 与其已 stage 状态完全一致。

本次顺序是：intent → root 密封 config / BASE handoff / post intent / unit → 启动 guardian → 正常 stage capture 和 post → builder partial plan → runner prepare 停在正式 FROZEN 边界 → 使用 STOP handoff 正常 stage prebridge → builder bound plan → runner refine-prebridge → continue-prepare。post workspace 仅以正式 staging-intent 角色准备，不领取或转移业务 transaction marker。runner prepare 对已经启动的同一 unit 使用幂等 `systemctl start`，再核对同一 guardian。兼容入口仍接受已具备三份合法真实 staged proof 的既有完整 seed；它不能用来宣称尚未 stage 的 prebridge 已经存在。

bridge 停止后，runner 的 `seal_post` 用 `post_intent` 调用正式 `seal-stopped`，补入实际 FROZEN bridge 的停止证明。正常 owner 验证 exact stopped-only refinement，原 staged plan 和 hash 保持不变。builder 不创建 controller state，也不制造已停止或已确认的事实。

补齐 seed 中实际远端 artifacts / helper 路径和 hash、prepare config、已暂存 plan，以及探针 URL、备份命令和本地克隆 / 回归选择，再生成 runner plan：

```sh
python3 scripts/build-credit-financial-plan.py plan \
  --seed /home/lightjunction/.cache/financial-private/seed.json \
  --intent /home/lightjunction/.cache/financial-private/intent.json \
  --output /home/lightjunction/.cache/financial-private/runner-plan.json
```

builder 会通过所绑定 runner 的实际 `validate_plan` 校验结果。新增 / 移除节点或修改数据库入口会改变 intent；不能沿用旧 intent。源文件、provider、generator、verifier 和 fingerprint generator 都须有真实本地 SHA。`generator_helpers` 列出 renderer 实际导入的全部 sibling modules。

完整 native seed 提供三条 `command_overrides`：`capture`、`prebridge_apply`、`post_apply`。复制正常 owner 已生成的完整目标侧 argv，保留包、rollback 包、probe / operator、版本、观察窗口及原有 backup 参数。前缀必须是实际 staged binary 的 `operator production ACTION`，不能使用伪造 controller state 或 `--plan` 替代。builder 只把 handoff path / SHA 换成正式停止封存结果的变量，并自动从同一 post apply argv 派生 `maintenance-retry`。post candidate 与兼容 bridge rollback 必须为同一 package SHA。

native prebridge 延后 stage 时，先正常 stage capture 和 post，保留两个真实 owner 的绑定。`owners.prebridge` 使用以下精确字段：`workspace`、`apply_contract`、`operator`、`staged_plan`、`stage_handoff`。后三项先全部为 JSON `null`；`apply_contract` 是 `{argv, timeout_seconds}`，填完整、已审阅的预期正常 owner apply argv。它只是对未来调用的承诺，不证明 workspace 或暂存文件已经存在。此时不提供 `command_overrides.prebridge_apply`，也不提供 prebridge confirm / stop / status override。

预期 argv 的 executable 是 `WORKSPACE/staging/lmm-api`，probe / operator binary 则都是 `WORKSPACE/staging/lmm-api-go`，两个 binary SHA 都等于最终 provider。参数顺序必须与正常 Go owner 一致：workspace、operator-user、四组 package 路径与 SHA、probe / operator 路径与 SHA、expected-version、observation-seconds、两个 handoff 参数、`--go-changed`，最后可带 `--preserve-edge-policy`。观察窗口显式为 120–360 秒。此延后路径仅支持正常 owner 的 backup disabled 选择，不接受任何 backup flag；已有完整 staged seed 的行为保持兼容。builder 只将两个 handoff 参数规范化为 `{handoff_path}` / `{handoff_sha256}`，其余 argv 和 timeout 不变。

生成的 partial plan 明确标记 `prebridge_staging=pending`，每个延后 native node 具有 `prebridge_stage`：workspace、capture_workspace、apply_contract 和三项 null 绑定。它省略恰好四条 prebridge apply / confirm / stop / status 命令，也不要求该 owner 的 staged operator / plan artifacts。runner 完成 capture → 全部 admission close → 全部 writer stop → 正式 prebridge handoff seal 后停在 `FROZEN_AWAITING_PREBRIDGE_STAGE`。

此时使用正式停止 handoff，通过正常 owner plan / stage 入口暂存 prebridge。正常 controller 的 STOP handoff loader 还会在本地按 handoff 中的绝对路径读取 archived environment、shutdown journal 和 capture receipt，逐一核对 SHA 与安全权限。正常 plan 前必须通过已有安全复制方式提供这三份真实证据的本地副本，不能沿用 BASE / post-intent 阶段只镜像 prepare config 的前提；archived environment 可能含凭据，不能写到 stdout、argv 或日志。handoff 文件本身可使用本地输出路径，但上述证据和 prepare config 要满足 loader 对原绝对路径的读取要求。

`node.handoff` 仍为原 BASE；不要替换它。将同一 seed 的 `owners.prebridge.operator`、`staged_plan`、`stage_handoff` 三项补成真实 `{path, sha256}`，另补正常 stage 产生的 `command_overrides.prebridge_apply`。`stage_handoff.path` 必须是正式 native 路径 `/var/lib/lmm-api-go-deploy/handoffs/SHA256.json`。保留原 apply_contract；实际 argv 在只规范化 handoff 后必须逐项与它相等，timeout 也相等。

使用原 intent 和新的 output 路径再次生成 full plan。它标记 `prebridge_staging=bound`，只补这四条命令、两份 staged operator / plan artifacts 和三项真实 metadata 绑定。runner 的 `refine-prebridge` 按白名单比较两份计划并核实实际正常 stage；随后 `continue-prepare` 才可启动 bridge。任何包、provider、backup 选择、观察窗口、workspace 或 BASE 变化均不能作为这次 refinement 接受。

下面使用同一个私有 work 目录。先用 pending seed 生成 `runner-partial.json`，填入实际 partial SHA 后运行 prepare；它必须停在 `FROZEN_AWAITING_PREBRIDGE_STAGE`：

```sh
python3 scripts/run-credit-financial-maintenance.py prepare \
  --plan /home/lightjunction/.cache/financial-private/runner-partial.json \
  --plan-sha256 REPLACE_WITH_PARTIAL_PLAN_SHA256 \
  --work /home/lightjunction/.cache/financial-private/work --confirm api.lmm.best
```

完成 STOP evidence 本地复制和真实 prebridge stage 后，用实际 bound seed 生成新文件，替换两个 SHA，再 refinement 和继续：

```sh
python3 scripts/build-credit-financial-plan.py plan \
  --seed /home/lightjunction/.cache/financial-private/seed-bound.json \
  --intent /home/lightjunction/.cache/financial-private/intent.json \
  --output /home/lightjunction/.cache/financial-private/runner-bound.json
python3 scripts/run-credit-financial-maintenance.py refine-prebridge \
  --plan /home/lightjunction/.cache/financial-private/runner-partial.json \
  --plan-sha256 REPLACE_WITH_PARTIAL_PLAN_SHA256 \
  --refined-plan /home/lightjunction/.cache/financial-private/runner-bound.json \
  --refined-plan-sha256 REPLACE_WITH_BOUND_PLAN_SHA256 \
  --work /home/lightjunction/.cache/financial-private/work --confirm api.lmm.best
python3 scripts/run-credit-financial-maintenance.py continue-prepare \
  --plan /home/lightjunction/.cache/financial-private/runner-bound.json \
  --plan-sha256 REPLACE_WITH_BOUND_PLAN_SHA256 \
  --work /home/lightjunction/.cache/financial-private/work --confirm api.lmm.best
```

其余命令自动生成。native confirm 自带正常观察契约，不加不存在的 `--wait`；systemd confirm 使用 `--wait --json`。systemd 不会输出 native 的 `MAINTENANCE_PREARM_FAILED`，其必需的 `post_retry` 槽位使用只读 status。barrier body hash 自动取 `lmm-credit-transition:TRANSITION_ID` 的 SHA。`backup_commands` 的 copy 从 stdin 读取完整 archive，verify 须输出 `backup_sha256` 和 `size_bytes` JSON；它们仍由 root 提供已审阅的真实命令。

实际 origin 的证书使用 `api.lmm.best` 时，node 可提供只读实核的 `probe_resolve_address` IP，并将 `probe_urls` 保持为 `https://api.lmm.best/...`。builder 将该 IP 原样封入完整 plan，由 runner 的真实 validator 检查；runner 用固定 curl `--resolve` 保留 Host、SNI 和 TLS 验证。public probes 仍使用正常域名解析。不要填 controller 本机 loopback URL 或关闭证书检查。

每个节点预先安装密封 root-owned guardian unit，执行路径与 hash 对应 seed 中的 `helpers.guardian` / `handoff`。将实际 unit 文件的 SHA 加入 `node.artifacts`。builder 仅生成 `systemctl start / stop UNIT`；runner 会只读等待初始 guardian 就绪，并检查 unit invocation、PID 和三锁身份。下面是 unit 模板，替换三个路径 / hash，不需要 enable：

```ini
[Unit]
Description=LMM credit transition guardian

[Service]
Type=simple
User=root
Group=root
UMask=0077
Restart=no
ExecStart=/usr/bin/python3 /var/lib/lmm-credit-transition/TRANSITION/maintenance-deploy-guardian.py serve --handoff /var/lib/lmm-credit-transition/TRANSITION/base-handoff.json --handoff-sha256 BASE_SHA256
```

receipt 目录须预先 root-owned 且不可被其他用户写入。`helpers.runner` 的远端 SHA 必须与本地 `controller` 相同，`helpers.systemd_owner` 仅在 systemd 节点需要。optional `node.cleanup` 可继承已审阅的正式 cleanup argv；root 的顺序是节点 admission release 后、guardian stop 前清理已有 ≥24h 的历史 payload。

独立 worktree 中的离线测试可显式选择最终 runner schema：

```sh
CREDIT_FINANCIAL_RUNNER_SCHEMA=/ABSOLUTE/REVIEWED/run-credit-financial-maintenance.py \
  PYTHONDONTWRITEBYTECODE=1 python3 scripts/test-build-credit-financial-plan.py
```
