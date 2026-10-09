from pathlib import Path
import subprocess
import sys

r = Path(sys.argv[1]).resolve()
def replace(path, old, new):
    p = r / path
    s = p.read_text()
    assert s.count(old) == 1, (path, s.count(old))
    p.write_text(s.replace(old, new))

replace('apps/web/src/features/support-ticket/support-conversation.tsx', '''                  if (sent)
                    setMessage((current) =>
                      current === message ? '' : current
                    )''', '''                  if (sent) {
                    setMessage((current) =>
                      current === message ? '' : current
                    )
                  }''')
p = r / 'apps/web/src/features/assistant/assistant-activation-tool.test.tsx'
s = p.read_text()
s = s.replace("const { createRoot } = await import('react-dom/client')", "const { createRoot } = await import('react-dom/client')\nconst { createMemoryHistory, createRootRoute, createRoute, createRouter, Outlet, RouterProvider } = await import('@tanstack/react-router')")
s = s.replace('  const root = createRoot(container)\n  await act', '''  const root = createRoot(container)
  const rootRoute = createRootRoute({ component: Outlet })
  const statusRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/',
    component: () => <AssistantActivationTool onApproved={onApproved} />,
  })
  const supportRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/support',
    component: () => <p>Human support</p>,
  })
  const router = createRouter({
    routeTree: rootRoute.addChildren([statusRoute, supportRoute]),
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  await act''')
s = s.replace('<AssistantActivationTool onApproved={onApproved} />\n        </I18nextProvider>', '<RouterProvider router={router} />\n        </I18nextProvider>')
s = s.replace("      assert.match(view.container.textContent ?? '', /on hold/)", "      assert.match(view.container.textContent ?? '', /on hold/)\n      assert.equal(view.container.querySelector('a')?.getAttribute('href'), '/support')")
p.write_text(s)
replace('apps/web/src/features/todos/api.test.ts', "import { getTodos, markAllTodosRead, markTodoRead, type TodoItem } from './api'", "import { getTodos, markAllTodosRead, markTodoRead, type TodoItem, type TodoPage } from './api'")
replace('apps/web/src/features/todos/api.test.ts', '      return { data: { success: true, data: { page: 2 } } }', '''      const page: TodoPage = {
        items: [], page: 2, page_size: 50, total: 51, category: 'all',
        unread_count: 0, total_unread_count: 0, unread_by_category: {}, categories: [],
      }
      return { data: { success: true, data: page } }''')
p = r / 'apps/web/src/features/todos/api.test.ts'
p.write_text(p.read_text() + '''

test('retired applications disappear from a previous backend response and unread counts', async () => {
  const item = (category: TodoItem['category'], source_id: number): TodoItem => ({
    id: `${category}:${source_id}`, source_id, category, type: 'test',
    title: 'test', summary: '', read: false, created_at: 1, updated_at: 1,
  })
  const current = item('open_source_bounty_review', 2)
  const page: TodoPage = {
    items: [item('developer_access', 1), current], page: 1, page_size: 50,
    total: 2, category: 'all', unread_count: 2, total_unread_count: 2,
    unread_by_category: { developer_access: 1, open_source_bounty_review: 1 },
    categories: [{ key: 'developer_access', total: 1, unread: 1 }, { key: 'open_source_bounty_review', total: 1, unread: 1 }],
  }
  api.get = (async () => ({ data: { success: true, data: page } })) as typeof api.get
  const result = await getTodos('all')
  assert.deepEqual(result.items, [current])
  assert.deepEqual(result.categories, [{ key: 'open_source_bounty_review', total: 1, unread: 1 }])
  assert.equal(result.unread_count, 1)
  assert.equal(result.total_unread_count, 1)
  assert.deepEqual(result.unread_by_category, { open_source_bounty_review: 1 })
  assert.equal(result.total, 2, 'Preserve page positions while the old backend is deployed')
})
''')
p = r / 'apps/web/src/features/todos/index.test.ts'
s = p.read_text().replace("      'DeveloperAccessRequestsPanel',\n", '')
s = s.replace("    assert.match(todosSource, /title=\\{t\\('L1 access requests'\\)\\}/)\n", '')
s = s.replace('    assert.match(\n      todosSource,\n      /initiallyExpanded=\\{focusDeveloperAccessId !== undefined\\}/\n    )\n', '')
s = s.replace("  test('uses the scrolling section layout', () => {", """  test('never mounts or imports the retired L1 application review', () => {
    for (const source of [todosSource, usersSource]) {
      assert.doesNotMatch(source, /DeveloperAccessRequestsPanel|L1 access requests|focusDeveloperAccessId/)
    }
  })

  test('uses the scrolling section layout', () => {""")
p.write_text(s)
p = r / 'apps/api-go/controller/assistant_retry_test.go'
s = p.read_text()
for name in ['TestAssistantRetryDoesNotDuplicateFirstTurnConversationOnReplay', 'TestAssistantClientTurnReplaysSavedReplyWithoutTimeOrAttemptHeuristics']:
    a = s.index('func ' + name + '(')
    b = s.find('\nfunc ', a + 5)
    b = len(s) if b < 0 else b
    t = s[a:b]
    assert t.count('Group:    "default",') == 1
    t = t.replace('Group:    "default",', 'Group:    "default",\n        // L0 admission tools must use live checks, not a cached natural-language answer.\n        ConsoleActivatedAt: common.GetTimestamp(),')
    t = t.replace('userContext := assistantUserContextForRequest(user.Id, message)', 'userContext := assistantUserContextForRequest(user.Id, message)\n    require.True(t, userContext.DeveloperAccessGranted)')
    s = s[:a] + t + s[b:]
p.write_text(s)
p = r / 'apps/api-go/controller/assistant_test.go'
s = p.read_text()
a = s.index('func TestPrepareAssistantRequestCacheHitSkipsDuplicateIntentWrite(')
b = s.index('\nfunc ', a + 5)
t = s[a:b]
t = t.replace('require.NoError(t, db.AutoMigrate(&model.AssistantLead{}, &model.AssistantProfileBucket{}, &model.AssistantFirstQuestionStat{}))', '''require.NoError(t, db.AutoMigrate(&model.AssistantLead{}, &model.AssistantProfileBucket{}, &model.AssistantFirstQuestionStat{}, &model.UserOAuthBinding{}, &model.AssistantUserProfile{}, &model.TopUp{}, &model.DeveloperAccessRequest{}))
    user := model.User{Id: 42, Username: "cache-active-user", Password: "password", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default", ConsoleActivatedAt: common.GetTimestamp()}
    require.NoError(t, db.Create(&user).Error)''')
t = t.replace('context := assistantUserContextForRequest(42, message)', 'context := assistantUserContextForRequest(42, message)\n    require.True(t, context.DeveloperAccessGranted)')
s = s[:a] + t + s[b:]
p.write_text(s)
a = s.index('func TestPrepareAssistantRequestRecommendationEditBypassesCachedAnswer(')
b = s.index('\nfunc ', a + 5)
t = s[a:b].replace('TestPrepareAssistantRequestRecommendationEditBypassesCachedAnswer', 'TestPrepareAssistantRequestL0FirstTurnBypassesCachedAnswer')
t = t.replace('message := "请帮我重写这封推荐信 " + t.Name()', 'message := "制作开源软件 " + t.Name()')
t = t.replace('require.Equal(t, assistantRecommendationActionRevise, context.RecommendationAction)', 'require.Equal(t, assistantRecommendationActionNone, context.RecommendationAction)\n    require.True(t, assistantDirectL1GrantAllowed(context))\n    require.Zero(t, context.CompletedAssistantTurns)')
p.write_text(p.read_text() + '\n' + t)
replace('apps/api-go/controller/assistant_context.go', '// user/assistant pairs. It gates the narrow L0 direct-grant tool and never\n\t// trusts transcript messages supplied by the browser.', '// user/assistant pairs for context and audit, not an L1 eligibility gate.\n\t// It never trusts transcript messages supplied by the browser.')
paths = subprocess.check_output(['git', 'diff', '--name-only'], cwd=r, text=True).splitlines()
assert len(paths) == 7 and all(p.startswith(('apps/web/src/', 'apps/api-go/controller/')) for p in paths)
subprocess.run(['git', 'diff', '--check'], cwd=r, check=True)
print('\n'.join(paths))
