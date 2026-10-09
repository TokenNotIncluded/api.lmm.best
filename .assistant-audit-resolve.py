import pathlib, json, re, subprocess
root = pathlib.Path('.')
conflicts = subprocess.check_output(['git', 'diff', '--name-only', '--diff-filter=U'], text=True).splitlines()
stages = {name: {str(i): subprocess.check_output(['git', 'show', f':{i}:{name}'], text=True) for i in (1, 2, 3)} for name in conflicts}
def p(n): return root / n
def replace(n, a, b):
    s = p(n).read_text(); assert a in s, (n, a); p(n).write_text(s.replace(a, b))
def blocks(n, fn):
    s = p(n).read_text()
    s = re.sub(r'<<<<<<< HEAD\n([\s\S]*?)=======\n([\s\S]*?)>>>>>>> b009976199d4441f4e8b465db2c623e9008ba056\n', lambda m: fn(m.group(1), m.group(2)), s)
    assert '<<<<<<< HEAD' not in s, n
    p(n).write_text(s)
def agent(a, b):
    if a.startswith('\tdefinitions ='): return a + b
    if a.startswith('\tif name =='): return '\tif name == "get_site_policy" || name == "search_site_policies" || name == "discover_tools" || name == "end_conversation" {\n'
    assert b.startswith('\teffect :=')
    return b
blocks('apps/api-go/controller/assistant_agent.go', agent)
blocks('apps/api-go/controller/assistant_tool_policy_test.go', lambda a, b: '\tassert.Len(t, registered, 72)\n\tassert.Equal(t, map[string]int{"read_only": 42, "confirmation": 16, "server_guarded": 13, "navigation": 1}, effects)\n')
blocks('apps/web/src/features/assistant/assistant-tool-calls.tsx', lambda a, b: a + b)
for name, data in stages.items():
    if not name.endswith('.json'): continue
    base, ours, theirs = [json.loads(data[str(i)]) for i in (1, 2, 3)]
    def merge(b, a, c):
        if a == c: return a
        if a == b: return c
        if c == b: return a
        if all(isinstance(x, dict) for x in (b, a, c)):
            r = dict(c)
            for k, v in a.items():
                if k not in c: r[k] = v
                elif k not in b:
                    assert c[k] == v, (name, k)
                    r[k] = v
                else: r[k] = merge(b[k], v, c[k])
            return r
        raise ValueError((name, b, a, c))
    p(name).write_text(json.dumps(merge(base, ours, theirs), ensure_ascii=False, indent=2) + '\n')
name = 'docs/assistant-tool-policy.md'
s = stages[name]['3']
s = s.replace('69 个工具，按 16 组', '72 个工具，按 17 组').replace('69 个禁用模拟调用', '72 个禁用模拟调用')
s = s.replace('40 个工具只读或展示数据，15 个工具准备确认表单', '42 个工具只读或展示数据，16 个工具准备确认表单')
pos = s.index('\n42 个工具只读')
s = s[:pos].rstrip() + '\n| 站内政策（3） | `get_site_policy`, `search_site_policies`, `prepare_admin_site_policy_change` | 读取、搜索已配置政策；超级管理员准备精确变更，用户在浏览器确认后才发布。 |\n\n' + s[pos:].lstrip()
s = s.replace('新功能包括 Go 数据表迁移，需要配套部署 Go 和 Web。', '既有 issue 和市场接入包括 Go 数据表迁移；本次工具修复和政策工具不新增数据表。需要配套部署 Go 和 Web。')
s += '\n## 配置与报错修复' + stages[name]['2'].split('## 配置与报错修复', 1)[1]
s = s.replace('也不覆盖既有 `/terms` 内容', '既有 `/terms` 和 `/terms-of-service` 仍跳转到用户协议，不生成第二份条款')
p(name).write_text(s)
replace('apps/api-go/controller/assistant_test.go', 'require.Len(t, definitions, 69)', 'require.Len(t, definitions, 72)')
replace('apps/api-go/setting/assistant_tool_audit_test.go', 'require.Equal(t, 70, count)', 'require.Equal(t, 72, count)')
replace('apps/web/src/features/system-settings/content/assistant-tool-policy-editor.test.tsx', 'assert.equal(registeredTools.length, 70)', 'assert.equal(registeredTools.length, 72)')
replace('apps/api-go/controller/assistant_persona_matrix_test.go', '"navigate_to_page",', '"navigate_to_page", "get_site_policy", "search_site_policies",')
name = 'apps/api-go/controller/assistant_site_policy.go'
replace(name, 'const assistantAdminSitePolicyChangeKind = "site_policy"', 'const assistantAdminSitePolicyChangeKind = "site_policy"\n\n// Legal documents have a bounded larger input budget than ordinary tool calls.\nconst assistantSitePolicyArgumentsMaxBytes = 192 << 10')
replace(name, '"old_text"] = map[string]any{"type": "string", "minLength": 1,', '"old_text"] = map[string]any{"type": "string", "minLength": 1, "maxLength": assistantAdminMaxValueRunes,')
replace(name, '"new_text"] = map[string]any{"type": "string",', '"new_text"] = map[string]any{"type": "string", "maxLength": assistantAdminMaxValueRunes,')
replace(name, ' The separate legacy /terms page is not this refund-policy setting.', ' /terms and /terms-of-service are aliases of the user agreement, not separate documents.')
replace(name, ', "legacy_terms_path": "/terms"', '')
replace(name, ', "legacy_terms_not_searched": true', '')
replace(name, 'if !a || !b || oldText == "" || strings.Count(values[key], oldText) != 1 {', 'if !a || !b || oldText == "" || utf8.RuneCountInString(oldText) > assistantAdminMaxValueRunes || utf8.RuneCountInString(newText) > assistantAdminMaxValueRunes || strings.Index(values[key], oldText) < 0 || strings.Index(values[key], oldText) != strings.LastIndex(values[key], oldText) {')
replace(name, '// Static legacy /terms content is deliberately not imported or rewritten.', '// The existing /terms and /terms-of-service routes remain user-agreement aliases.')
name = 'apps/api-go/controller/assistant_agent.go'
replace(name, 'if len(arguments) > assistantToolArgumentsMaxBytes {\n\t\treturn map[string]any{"ok": false, "error": "tool arguments are too large"}\n\t}', '''argumentLimit := assistantToolArgumentsMaxBytes
	if name == "prepare_admin_site_policy_change" {
		argumentLimit = assistantSitePolicyArgumentsMaxBytes
	}
	if len(arguments) > argumentLimit {
		return map[string]any{"ok": false, "status": "invalid_arguments", "error": "tool arguments exceed this tool's size limit"}
	}''')
name = 'apps/api-go/controller/assistant_site_policy_test.go'
replace(name, '"same same")', '"same same aaaa")')
replace(name, '{"old_text": "same", "new_text": "replacement"},', '{"old_text": "same", "new_text": "replacement"}, {"old_text": "aaa", "new_text": "b"},')
p(name).write_text(p(name).read_text() + '''
func TestAssistantSitePolicyFullChineseContentFitsExecutionBudget(t *testing.T) {
	c, user := assistantPolicyContentTestContext(t, common.RoleRootUser)
	c.Set(assistantUserContextKey, assistantUserContext{AdministratorMode: true, AccessLevel: "ROOT"})
	read := executeAssistantGetSitePolicy(c, map[string]any{"document": "privacy_policy"})
	content := strings.Repeat("隐私政策", 3000)
	arguments, err := json.Marshal(map[string]any{"document": "privacy_policy", "language": "zh-CN", "revision": read["revision"], "content": content})
	require.NoError(t, err)
	require.Greater(t, len(arguments), assistantToolArgumentsMaxBytes)
	result := executeAssistantTool(c, assistantOpenAIToolCall{Function: assistantOpenAIToolCallFunction{Name: "prepare_admin_site_policy_change", Arguments: string(arguments)}})
	require.Equal(t, true, result["ok"], "%v", result)
	assert.Equal(t, false, result["applied"])
	action, exists := c.Get(assistantClientActionKey)
	require.True(t, exists)
	assert.Equal(t, content, action.(map[string]any)["changes"].([]assistantAdminConfigPreview)[0].NewValue)
	values, err := model.ReadSitePolicies(context.Background())
	require.NoError(t, err)
	assert.Empty(t, values["legal.privacy_policy"], "a tool call cannot publish a document")
	assert.Positive(t, user.Id)
}
''')
for path in root.rglob('*'):
    if not path.is_file() or '.git' in path.parts: continue
    if path.suffix in ('.go', '.md', '.ts', '.tsx', '.json'):
        assert '<<<<<<< HEAD' not in path.read_text(), path
