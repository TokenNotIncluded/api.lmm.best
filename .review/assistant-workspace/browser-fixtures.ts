// Temporary browser review only. Never part of the production entry or feature commit.
import { api } from '@/lib/http-client'
import catalog from './workspace-review-catalog.json'

export function installWorkspaceReviewFixtures() {
  const previous = api.defaults.adapter
  if (typeof previous !== 'function') throw new Error('Local persona adapter required')
  let preference = { templates: {} as Record<string, string>, revision: 0, updated_at: 0 }
  let policy = '{"version":1,"groups":{},"tools":{}}'
  const writes: unknown[] = []
  Object.assign(window, { workspaceReviewWrites: writes })
  api.defaults.adapter = async (config) => {
    const path = new URL(config.url ?? '', location.origin).pathname
    const method = (config.method ?? 'get').toUpperCase()
    const response = (data: unknown) => ({ data: { success: true, data }, status: 200, statusText: 'OK', headers: {}, config })
    if (path === '/api/assistant/admin/tool-catalog') return response({ groups: catalog })
    if (path === '/api/assistant/workspace/greeting') {
      if (method === 'PUT') {
        const body = typeof config.data === 'string' ? JSON.parse(config.data) : config.data
        if (body.revision !== preference.revision) throw new Error('Review revision conflict')
        writes.push({ path, body })
        preference = { templates: { ...preference.templates, [body.language]: body.template }, revision: preference.revision + 1, updated_at: Math.floor(Date.now()/1000) }
      }
      return response(preference)
    }
    if (path === '/api/data/self') {
      const params = config.params ?? {}
      const start = Number(params.start_timestamp), end = Number(params.end_timestamp)
      const rows = Array.from({ length: Math.ceil((end-start)/86400) }, (_, index) => ({ created_at: Math.min(end, start+index*86400+3600), model_name: ['Review model A', 'Review model B', 'Review model C'][index%3], count: [180,240,210,360,280,410,390][index%7], token_used: 40000+index*8100, quota: 25000+index*3000 }))
      return response(rows)
    }
    if (path === '/api/tool-market/services') return response([{ id: 'review-service', name: '文档检索 · 演示服务', description: '隔离测试数据，不连接外部服务。', execution_type: 'remote' }])
    if (path === '/api/option/' && method === 'GET') {
      const old = await previous(config)
      return { ...old, data: { success: true, data: [...old.data.data.filter((row: { key: string }) => !row.key.startsWith('Assistant')), {key:'AssistantToolPolicy',value:policy}, {key:'AssistantEnabled',value:'false'}, {key:'AssistantNewUserGiftMaxCredits',value:'5000000'}] } }
    }
    if (path === '/api/option/' && method === 'PUT') {
      const body = typeof config.data === 'string' ? JSON.parse(config.data) : config.data
      writes.push({path,body})
      if (body.values?.AssistantToolPolicy) policy = body.values.AssistantToolPolicy
      return response(null)
    }
    return previous(config)
  }
}
