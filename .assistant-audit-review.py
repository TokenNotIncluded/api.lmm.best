from pathlib import Path


def replace(path, old, new):
    target = Path(path)
    text = target.read_text()
    assert text.count(old) == 1, (path, old[:80], text.count(old))
    target.write_text(text.replace(old, new))


replace('apps/api-go/controller/assistant_run_control_test.go',
        'assistantUserContext{AccessLevel: "L0", LatestUserRequest: "What is 2 + 2?"}',
        'assistantUserContext{AccessLevel: "L0", Intent: model.AssistantIntentMath, LatestUserRequest: "What is 2 + 2?"}')
replace('apps/api-go/setting/assistant_tool_policy_test.go',
        '\tgroups := AssistantToolCatalogue()\n\tgroups[0].ID = "corrupt"',
        '\tgroups := AssistantToolCatalogue()\n\tfirstTool := groups[0].Tools[0].Name\n\tgroups[0].ID = "corrupt"')
replace('apps/api-go/setting/assistant_tool_policy_test.go',
        'AssistantToolCatalogue()[0].Tools[0].Name != "get_service_facts"',
        'AssistantToolCatalogue()[0].Tools[0].Name != firstTool')
replace('apps/api-go/controller/assistant_agent_recovery_test.go',
        '''\t\t\tfor _, id := range []string{"", " padded-call "} {
\t\t\t\tcalls = append(calls, assistantOpenAIToolCall{
\t\t\t\t\tID: id, Type: "function", Function: assistantOpenAIToolCallFunction{Name: "calculate_math", Arguments: `{"expression":"1+1"}`},
\t\t\t\t})
\t\t\t}''',
        '''\t\t\tfor index, id := range []string{"", " padded-call "} {
\t\t\t\t// Distinct work isolates ID repair from duplicate-read protection.
\t\t\t\tcalls = append(calls, assistantLoopMathCall(id, index+1))
\t\t\t}''')
replace('apps/api-go/controller/assistant_persona_matrix_test.go',
        '\t\t\texpectedAllowed = append(expectedAllowed,\n',
        '\t\t\texpectedAllowed = append(expectedAllowed,\n\t\t\t\t"discover_tools", "end_conversation",\n')
p = Path('apps/api-go/controller/assistant_tool_policy_test.go')
s = p.read_text()
marker = 'func TestAssistantToolPolicyRevokedDuringModelCallDoesNotWriteMemory(t *testing.T) {'
assert s.count(marker) == 1
before, test = s.split(marker)
old = '\t\tif turns == 1 {\n\t\t\trequire.True(t, assistantPolicyTestContainsTool(request.Tools, "remember_memory"))'
new = '''\t\tif turns == 1 {
\t\t\tassert.False(t, assistantPolicyTestContainsTool(request.Tools, "remember_memory"))
\t\t\treturn http.StatusOK, assistantLoopCallBody(t, []assistantOpenAIToolCall{assistantControlCall("discover_tools", `{"names":["remember_memory"]}`)}, ""), nil
\t\t}
\t\tif turns == 2 {
\t\t\trequire.True(t, assistantPolicyTestContainsTool(request.Tools, "remember_memory"))'''
assert test.count(old) == 1
assert test.count('assert.Equal(t, 2, turns)') == 1
test = test.replace(old, new).replace('assert.Equal(t, 2, turns)', 'assert.Equal(t, 3, turns)')
p.write_text(before + marker + test)
replace('docs/assistant-tool-policy.md', '| 奖励（5） | `get_invitation_rewards`', '| 奖励（6） | `send_invitation`, `get_invitation_rewards`')
replace('docs/assistant-tool-policy.md', '| 记忆与个性化（5） | `set_conversation_title`', '| 记忆与个性化（7） | `get_overview_greeting`, `set_overview_greeting`, `set_conversation_title`')
replace('docs/assistant-tool-policy.md', '查询连接地址、活动、客户端配置；提供站内链接；搜索、计算。', '按需加载工具、结束本轮；查询连接地址、活动、客户端配置；提供站内链接；搜索、计算。')
replace('docs/assistant-tool-policy.md', '\n40 个工具只读或展示数据', '''| 站内改进（3） | `get_site_issues`, `create_site_issue`, `update_site_issue` | 查询、提交及更新有权限访问的站内改进记录。 |
| 市场接入（3） | `get_connected_market_tools`, `connect_market_tool`, `call_market_tool` | 查询已接入工具、准备接入确认及调用已授权的远程工具。 |
| 可视化（4） | `show_chart`, `show_statistics`, `show_choices`, `show_flowchart` | 显示图表、指标、选项及流程；展示不会确认数据真实性或执行账户操作。 |

40 个工具只读或展示数据''')
