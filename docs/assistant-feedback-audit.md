# 内置助手会话反馈审计

`scripts/assistant_feedback_audit.py` 用 Python 标准库流式读取管理员授权导出的 JSONL，不调用模型或网络，不把全部原文送入模型上下文。临时 SQLite 负责排序和去重，结束时删除；输出目录权限为 `0700`，文件为 `0600`。原始会话、摘要、候选和语义审阅结果均放在仓库外的私有状态目录，不能提交到 Git 或作为公开测试样本。

## 输入和运行

每行是一条消息，字段为 `id`、`conversation_id`、`owner_key`、`sequence`、`role`、`content`、`created_at`。`owner_key` 必须由导出端匿名化，不能直接填写用户名、邮箱或用户 ID。按会话及 `sequence/id` 排序；脚本也会在 SQLite 中重新排序，并统计缺失 ID、非法序号、重复序号和未知角色。支持 `user/assistant/human/tool/system/secure_card`，安全卡片内容完全省略。

可选 `inventory.json` 的整数统计包括 `conversations/users/empty_conversations/first_created_at/last_updated_at/snapshot_at`。它描述留存记录；脚本单独报告实际有消息的会话与用户，避免将空会话和没有留存消息的用户算作已审阅。

```sh
python3 scripts/assistant_feedback_audit.py scan \
  --input /private/state/history.jsonl \
  --inventory /private/state/inventory.json \
  --output /private/state/audit
python3 -m unittest discover -s scripts -p 'test_assistant_feedback_audit.py'
```

## 产物与完整审阅

- `summary.json`：扫描总量、消息角色、去重前后会话与回合数、裁剪量、规则类别、每类至多 8 个匿名例子。
- `all_unique_turns.jsonl`：全部唯一回合，包含用户请求、助手及人工回复、代表匿名来源、重复次数、不同来源会话/用户数量、顺序、裁剪量、哈希分桶和规则来源。连续用户消息合并为一条请求；下一条用户消息在助手或人工已经回复时开始新回合。空助手回复不算已回答；人工回复单独标记，避免误判为未回答。
- `conversation_fragments.jsonl`：每个有消息的会话分成最多 8 个消息部分的片段；每部分最多 2000 字符。长消息连续拆分，用 `message_part/message_parts` 保留完整顺序，不舍弃中间内容。覆盖所有脱敏后的消息和角色，包括没有用户前序的助手消息。
- `candidates.jsonl`：规则命中回合，加上 16 个哈希桶中各至多 8 个候选。没有关键词的回合也能进入样本。候选用于快速定位，不能替代完整语义审阅。
- `review-00..05.jsonl`：全部去重会话的 6 个非重叠分片，包含原会话重复次数、角色和匿名 ID。每消息最多 1000 字符、每会话文本总量最多 3000 字符；长会话公平分配字符预算，各条都带裁剪量。超过 200 条消息的会话保留前后各 100 条，并明确统计省略消息数。裁剪或省略过的内容需要通过回合或完整有界片段补读。

对 6 个分片分别进行语义审阅，并记录每片会话数、实际审阅数和需要补读的会话。不能仅命中关键词、抽查候选，就宣称“所有会话都经过语义分析”。对于问题回合，补读同一个会话的所有片段，结合当前工具能力判断：是助手拒绝了可执行操作、工具执行失败、权限边界合理，还是已修复功能的历史旧回复。

```sh
# 所有 unique 回合可按 offset/limit 连续读取，单次最多 50 条。
python3 scripts/assistant_feedback_audit.py read \
  --input /private/state/audit/all_unique_turns.jsonl --offset 0 --limit 8
python3 scripts/assistant_feedback_audit.py read \
  --input /private/state/audit/all_unique_turns.jsonl --bucket 3 --offset 0 --limit 8
# 使用匿名 conversation_id 补齐长会话上下文。
python3 scripts/assistant_feedback_audit.py read \
  --input /private/state/audit/conversation_fragments.jsonl \
  --conversation ANONYMOUS_ID --offset 0 --limit 8
```

## 信号、去重与隐私边界

去重使用完整脱敏文本，进行 Unicode NFKC、大小写、空白规范化后取 SHA-256；同样的用户请求配上不同回复不会合并。会话指纹包含角色顺序，回合指纹分别包含用户、助手和人工回复。去重不使用 ID、时间或个人值，因此重复次数表示文本重复，不能推断重复账号或恶意行为。

规则覆盖拒绝/功能缺失、失败、权限申请、模型价格、支付、昵称资料、体验抱怨、人工转接和未回答；规则统计是线索，不是缺陷计数。`assistant_self_limitation` 只匹配助手内容，其余规则同时检查用户及助手并保留命中来源。权限说明、“不支持某模型”、正常人工审批都可能命中；未回答也可能来自取消、中断、保留策略或还在进行的请求。

输出先移除私钥、常见 API 密钥、JWT、认证头、敏感字段、URL（含路径/query/fragment）、邮箱、IP、电话长号码、私人路径和长不透明串，并通过明确昵称设置/姓名表达替换已知名字及同会话中的重复提及。自然语言个人信息不能靠正则完整识别，匿名键也不能保证抵抗猜测和外部关联，所以全部输出继续作为私有数据。对外报告只能写聚合数量、概括的问题和代码修复，不发布用户原文或匿名来源的映射。
