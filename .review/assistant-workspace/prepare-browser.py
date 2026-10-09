from pathlib import Path
import json
import re
import shutil
import sys

source = Path(sys.argv[1])
helpers = Path(__file__).parent
catalog = []
for line in (source/'apps/api-go/setting/assistant_tool_policy.go').read_text().splitlines():
    match = re.match(r'\s*\{"([a-z_]+)", "([^"]+)", \[\]AssistantToolInfo\{', line)
    if match:
        catalog.append({'id':match[1], 'label':match[2], 'tools':[]})
        continue
    match = re.match(r'\s*\{((?:"(?:[^"\\]|\\.)*",?\s*){5})\},', line)
    if match and catalog:
        values = json.loads('['+match[1].rstrip(', ')+']')
        catalog[-1]['tools'].append(dict(zip(['name','label','description','effect','access'],values)))
if sum(len(group['tools']) for group in catalog) != 67:
    raise ValueError('Unexpected real tool catalogue count')
debug = source/'apps/web/src/features/debug'
(debug/'workspace-review-catalog.json').write_text(json.dumps(catalog,ensure_ascii=False))
text = (helpers/'browser-fixtures.ts').read_text().replace("path === '/api/tool-market/services'", "path === '/api/tool-market'").replace("{key:'AssistantEnabled',value:'false'}", "{key:'AssistantEnabled',value:'true'}, {key:'AssistantSearchProvider',value:'exa'}")
visuals = [
 {'name':'show_statistics','kind':'statistics','title':'请求概况','items':[{'label':'本周请求','value':'1,284','detail':'隔离演示数据','icon':'activity'},{'label':'平均响应','value':'820 ms','detail':'隔离演示数据','icon':'clock'}]},
 {'name':'show_chart','kind':'chart','title':'模型请求趋势','chart_type':'line','labels':['周一','周二','周三','周四','周五','周六','周日'],'series':[{'name':'模型 A','values':[45,62,51,89,83,101,120]},{'name':'模型 B','values':[30,45,64,57,75,64,85]}]},
 {'name':'show_chart','kind':'chart','title':'用量分布','chart_type':'donut','labels':['模型 A','模型 B','其他'],'series':[{'name':'请求','values':[640,430,214]}]},
 {'name':'show_flowchart','kind':'flowchart','title':'工具授权流程','nodes':[{'id':'select','label':'选择工具'},{'id':'limits','label':'设置支出上限'},{'id':'confirm','label':'确认授权'},{'id':'call','label':'调用并记录用量'}],'edges':[{'from':'select','to':'limits'},{'from':'limits','to':'confirm'},{'from':'confirm','to':'call'}]},
 {'name':'show_choices','kind':'choices','title':'下一步','items':[{'label':'查看用量','value':'请查看我的用量','detail':'只填入输入框，不直接发送。'},{'label':'检查分组','value':'请检查我的分组','detail':'只填入输入框，不直接发送。'}]},
]
traces = [{'name':row['name'],'call_id':str(index),'status':'output-available','visualization':{k:v for k,v in row.items() if k!='name'}|{'source':'仅用于界面验收的隔离测试数据'}} for index,row in enumerate(visuals)]
payload = {'choices':[{'message':{'content':'以下图表使用隔离测试数据，不代表真实账户用量。'}}], 'lmm_assistant_tools':traces}
text += '\nexport function installWorkspaceReviewChat() {\nconst prior = globalThis.fetch.bind(globalThis)\nconst payload = '+json.dumps(payload,ensure_ascii=False)+'\n'
text += '''let calls = 0
Object.assign(window, { workspaceReviewChatCalls: () => calls })
globalThis.fetch = async (input, init) => {
 const raw = typeof input === 'string' || input instanceof URL ? String(input) : input.url
 const url = new URL(raw, location.origin)
 if (url.origin === location.origin && url.pathname === '/api/assistant/chat') {
  calls += 1
  return new Response(JSON.stringify(payload), { status: 200, headers: {'content-type':'application/json'} })
 }
 return prior(input, init)
}
}
'''
(debug/'workspace-review-fixtures.ts').write_text(text)
entry = source/'apps/web/src/debug-main.tsx'
text = entry.read_text()
text = "import { installWorkspaceReviewFixtures, installWorkspaceReviewChat } from './features/debug/workspace-review-fixtures'\n"+text
needle = "await import('./main')"
if text.count(needle) != 1: raise ValueError('Unexpected debug entry')
text = text.replace(needle, 'installWorkspaceReviewFixtures()\ninstallWorkspaceReviewChat()\n'+needle)
entry.write_text(text)
shutil.copy(helpers/'browser-review.mjs',source/'apps/web/scripts/assistant-workspace-browser-review.mjs')
