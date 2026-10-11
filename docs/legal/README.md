# 可配置的协议模板

提供用户协议（含服务条款）、隐私政策、退款政策的中文和英文 Markdown 草稿。模板是起点，不是针对某地区的法律意见。运营方应结合实际服务和适用要求审查；不要照抄不存在的退款承诺、保存期限或安全措施。

## 配置一次，独立分发

扩展分发包带有 `config/legal/site.example.toml` 和 `config/legal/templates/`。Docker 镜像与原生 Linux 包的只读资源位于 `/usr/share/lmm/legal`。它们不含站点名称、运营主体、联系方式、金额、退款天数或指定司法地区；`published` 默认为 `false`。运行配置遵循[固定 TOML 与目录规则](../filesystem-layout.md)。

1. 将 `site.example.toml` 另存为 `/etc/lmm.conf.d/legal.toml`。模板可使用已安装的只读目录，或复制到操作者独立维护的绝对路径；将 `template_root` 设置为该目录。文档路径相对于这个明确的根目录，不相对于当前工作目录。不要在软件包目录内直接改运营配置。
2. 填写所有被模板引用的 `variables`，编辑各语言的 Markdown。占位符只支持 `{{variable_name}}`，没有代码执行、文件包含或环境变量读取。字段不会自动翻译；不同语言的长条款可直接写进对应 Markdown。
3. 审查完成后将 `published` 设为 `true`，在 `/etc/lmm.conf.d/extensions.toml` 的 `[legal]` 中设置 `config_file="/etc/lmm.conf.d/legal.toml"`，明确重启扩展进程。无需重编译，也无需重启核心。

默认配置不能直接发布。缺少变量、空白条款、残留占位符、未知文档、缺少默认语言、越界路径、符号链接或过大文件都会使已启用的配置加载失败。配置文件最多 64 KiB，单篇文档最多 128 KiB；每个文档最多 8 种语言。

## 要填写什么

共同信息包括服务名、主体、地址、生效日期及联系渠道。用户协议还需实际服务范围、使用资格、计价和账户关闭规则。隐私政策需数据种类、用途、处理依据、接收方、处理地点、保存规则、安全措施、Cookie 与权利请求流程。退款政策需资格、例外、申请期限、流程、费用、续订取消和数字交付规则。

所有期限和条件均由运营方填写，不从某个支付渠道推导。模板不声称自动续订、退款、删除数据或同意记录功能已经接通。正式投入使用前，应请熟悉适用地区要求的专业人员审查。

## 读取与更新

保留前端已有响应结构：`{ "success": true, "data": "Markdown" }`。已发布内容还带 `language` 和内容 SHA-256 `revision`。

| GET / HEAD | 用途 |
| --- | --- |
| `/api/user-agreement?lang=zh-CN` | 用户协议与服务条款 |
| `/api/privacy-policy?lang=en` | 隐私政策 |
| `/api/refund-policy?lang=en-US` | 退款政策 |

语言先精确匹配，再尝试基础语言，最后回退到 `default_language`。省略文档即关闭该文档。未配置或 `published=false` 时返回空内容，不公开草稿。POST 等写入方法返回 405。

启动时校验并读取完整快照；请求不会访问文件或核心，更新需要重启扩展。保留之前已发布版本和生效记录，替换配置前先校验。此模块不记录用户同意，不改变退款执行规则，也不提供管理员写入 API。旧控制台 `legal.*` 设置尚未接回新宿主，不能把页面上的保存操作当成本配置已更新。

反向代理只把上述三个精确公共路径转给扩展；不要向浏览器开放 `/extensions/v1/`，也不要下发宿主服务凭证。旧 `/legal/terms.html`、`/legal/privacy.html` 和 `/legal/safety-review.html` 只保留本站页面跳转，不再携带写死的运营政策。

Docker 可追加 `deployment/docker/compose.legal.yml`，以只读挂载提供审查后的 TOML 和明确的模板根目录。`LMM_LEGAL_DIRECTORY` 等 Compose 插值变量只用于构造挂载，不是进程读取的运行配置；以当前 Compose 文件的明确契约为准。后续每次扩展更新都要保留此覆盖文件。模板随程序升级，运营方已发布的配置不会被覆盖。旧 `site.json` 与 `LMM_LEGAL_CONFIG_FILE` 属于旧版本流程，不能直接传给新的 TOML 解析器。
