from pathlib import Path
import json
p=Path('.')
def edit(f,a,b):
 q=p/f;s=q.read_text();assert s.count(a)==1,(f,s.count(a),a[:90]);q.write_text(s.replace(a,b))
f='apps/web/src/features/tool-market/api.ts'
edit(f,'  mcp_oauth: boolean','  mcp_oauth: boolean\n  metamcp: boolean')
edit(f,'export type MarketConfig = {','''export type MarketMetaTool = {
  name: string
  title?: string
  description: string
  inputSchema: Record<string, unknown>
}
export type MarketConfig = {
  meta_tool?: MarketMetaTool''')
(p/'apps/web/src/features/tool-market/meta-tool-card.tsx').write_text('''/*
Copyright (C) 2026 LIghtJUNction
SPDX-License-Identifier: AGPL-3.0-or-later
*/
import { Button } from '@/components/ui/button'

import { marketSupports, type MarketConfig } from './api'
import { useMarketTranslation as useTranslation } from './provider-i18n'

export function MarketMetaToolCard({
  config,
  onConnect,
}: {
  config?: MarketConfig
  onConnect: () => void
}) {
  const { t } = useTranslation()
  const tool = config?.meta_tool
  if (!marketSupports(config, 'metamcp') || tool?.name !== 'metamcp') return null
  return (
    <section
      aria-labelledby='market-meta-title'
      className='bg-primary/5 min-w-0 space-y-5 rounded-2xl p-5 sm:p-6'
    >
      <div className='flex flex-col items-start justify-between gap-4 sm:flex-row'>
        <div className='min-w-0 space-y-2'>
          <p className='text-muted-foreground text-xs'>{t('Built-in gateway tool')}</p>
          <h3 id='market-meta-title' className='text-xl font-semibold tracking-tight'>metamcp</h3>
          <p className='text-muted-foreground max-w-2xl text-sm leading-6'>
            {t('Available after connecting. Discover tools, inspect their details, then load and invoke them through one entry.')}
          </p>
        </div>
        <Button className='min-h-11 shrink-0' onClick={onConnect}>
          {t('Connect MCP client')}
        </Button>
      </div>
      <p className='text-sm leading-7'>
        {t('Discover → Inspect → Load → Authorize → Invoke')}
      </p>
      <p className='text-muted-foreground text-xs leading-6'>
        {t('Management is free. Invocation uses the selected tool price. Loading does not grant payment permission.')}
      </p>
      <details className='min-w-0 text-sm'>
        <summary className='cursor-pointer py-2'>{t('View actions and parameters')}</summary>
        <pre className='bg-background/70 mt-3 max-h-80 max-w-full overflow-auto rounded-xl p-4 text-xs leading-6' tabIndex={0}>
          {JSON.stringify(tool.inputSchema, null, 2)}
        </pre>
      </details>
    </section>
  )
}
''')
f='apps/web/src/features/tool-market/index.tsx'
edit(f,"import { MarketConnections } from './connections'", "import { MarketConnections } from './connections'\nimport { MarketMetaToolCard } from './meta-tool-card'")
edit(f,"<TabsContent value='market' className='space-y-5 pt-4'>", """<TabsContent value='market' className='space-y-5 pt-4'>
                    <MarketMetaToolCard config={config.data} onConnect={() => chooseTab('connections')} />""")
(p/'apps/web/src/features/tool-market/oauth-connection.tsx').write_text(r'''/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import { useState } from 'react'

import { Button } from '@/components/ui/button'

import { marketEndpoint } from './connection-utils'
import { useMarketTranslation as useTranslation } from './provider-i18n'

export function marketOAuthCommand(endpoint: string): string {
  const url = new URL(endpoint)
  const modes = url.searchParams.getAll('mode')
  if (
    !/^https?:\/\/[a-zA-Z0-9.:[\]-]+\/[a-zA-Z0-9/_-]+(?:\?mode=(?:compact|full))?$/.test(endpoint) ||
    `${url.origin}${url.pathname}` !== marketEndpoint(url.origin, url.pathname) ||
    (url.search !== '' && (modes.length !== 1 || !['compact', 'full'].includes(modes[0])))
  )
    throw new Error('Invalid MCP endpoint')
  // Quote query punctuation and IPv6 brackets rather than exposing shell globs.
  const target = url.search || endpoint.includes('[') ? `'${endpoint}'` : endpoint
  return `codex mcp add lmm --url ${target}\n# Complete browser authorization. If needed, run:\ncodex mcp login lmm`
}

export function MarketOAuthConnection({ endpoint }: { endpoint: string }) {
  const { t } = useTranslation()
  const [compact, setCompact] = useState(true)
  const [status, setStatus] = useState<{ value: string; failed: boolean } | null>(null)
  // The parent supplies a validated, query-free server path.
  const target = compact ? `${endpoint}?mode=compact` : endpoint
  const command = marketOAuthCommand(target)
  const copy = async (value: string) => {
    try {
      await navigator.clipboard.writeText(value)
      setStatus({ value, failed: false })
    } catch {
      setStatus({ value, failed: true })
    }
  }
  const visibleStatus = status && [target, command].includes(status.value) ? status : null
  return (
    <div className='min-w-0 space-y-4'>
      <h4 className='font-medium'>{t('Browser login (recommended)')}</h4>
      <p className='text-muted-foreground text-sm leading-6'>
        {t('Add this server URL in an MCP client, choose OAuth, and approve access in your browser. No pasted token is needed.')}
      </p>
      <div role='group' aria-label={t('MCP tool list')} className='flex flex-wrap gap-2'>
        <Button variant={compact ? 'default' : 'outline'} aria-pressed={compact} className='min-h-11' onClick={() => setCompact(true)}>
          {t('Compact: metamcp only')}
        </Button>
        <Button variant={!compact ? 'default' : 'outline'} aria-pressed={!compact} className='min-h-11' onClick={() => setCompact(false)}>
          {t('Full: individual tools too')}
        </Button>
      </div>
      <div className='flex min-w-0 flex-col items-start gap-3 sm:flex-row sm:items-center'>
        <code className='bg-muted min-w-0 flex-1 break-all rounded-lg p-3 text-sm'>{target}</code>
        <Button variant='outline' className='min-h-11 shrink-0' onClick={() => void copy(target)}>{t('Copy server URL')}</Button>
      </div>
      <p className='text-muted-foreground text-sm leading-6'>
        {t('Run the command, sign in to LMM, and approve access. Your provider key is not sent to the client.')}
      </p>
      <pre className='bg-muted max-w-full overflow-x-auto rounded-lg p-4 text-xs leading-6' tabIndex={0}>{command}</pre>
      <Button variant='outline' className='min-h-11' onClick={() => void copy(command)}>{t('Copy command')}</Button>
      {visibleStatus && <p role={visibleStatus.failed ? 'alert' : 'status'}>{t(visibleStatus.failed ? 'Copy failed' : 'Copied')}</p>}
      <p className='text-muted-foreground text-xs leading-5'>
        {t('After login, select this OAuth client and set tool access and spending limits. Login alone does not authorize spending.')}
      </p>
    </div>
  )
}
''')
rows=[
['Built-in gateway tool','内置入口工具','內建入口工具','Outil intégré de la passerelle','内蔵ゲートウェイツール','Встроенный инструмент шлюза','Công cụ cổng tích hợp'],
['Available after connecting. Discover tools, inspect their details, then load and invoke them through one entry.','连接后即可使用。通过一个入口发现工具、查看详情、加载并执行。','連線後即可使用。透過一個入口探索工具、查看詳情、載入並執行。','Disponible après connexion. Découvrez les outils, consultez leurs détails, puis chargez-les et exécutez-les depuis une seule entrée.','接続後に利用できます。1 つの入口でツールの検索、詳細確認、読み込み、実行ができます。','Доступен после подключения. Находите инструменты, смотрите описание, загружайте и вызывайте через единый вход.','Dùng sau khi kết nối. Tìm công cụ, xem chi tiết, tải và gọi qua một điểm truy cập.'],
['Connect MCP client','接入 MCP 客户端','接入 MCP 用戶端','Connecter un client MCP','MCP クライアントを接続','Подключить клиент MCP','Kết nối ứng dụng MCP'],
['Discover → Inspect → Load → Authorize → Invoke','发现 → 详情 → 加载 → 授权 → 执行','探索 → 詳情 → 載入 → 授權 → 執行','Découvrir → Examiner → Charger → Autoriser → Exécuter','検索 → 詳細 → 読み込み → 許可 → 実行','Поиск → Описание → Загрузка → Разрешение → Вызов','Tìm → Xem → Tải → Cấp quyền → Gọi'],
['Management is free. Invocation uses the selected tool price. Loading does not grant payment permission.','管理操作免费。执行按所选工具计费。加载不等于授权付费。','管理操作免費。執行依所選工具計費。載入不等於授權付費。','La gestion est gratuite. L’exécution suit le tarif de l’outil. Le chargement n’autorise pas le paiement.','管理操作は無料です。実行には選択したツールの料金がかかります。読み込みは支払いの許可ではありません。','Управление бесплатно. Вызов оплачивается по цене инструмента. Загрузка не разрешает оплату.','Quản lý miễn phí. Gọi theo giá công cụ đã chọn. Tải không cấp quyền thanh toán.'],
['View actions and parameters','查看操作与参数','查看操作與參數','Voir les actions et paramètres','操作とパラメーターを表示','Действия и параметры','Xem thao tác và tham số'],
['Add this server URL in an MCP client, choose OAuth, and approve access in your browser. No pasted token is needed.','在 MCP 客户端添加此服务器地址，选择 OAuth，再到浏览器确认授权。不需要粘贴令牌。','在 MCP 用戶端新增此伺服器位址，選擇 OAuth，再到瀏覽器確認授權。不需要貼上權杖。','Ajoutez cette URL dans un client MCP, choisissez OAuth et autorisez l’accès dans le navigateur. Aucun jeton à coller.','MCP クライアントにこの URL を追加し、OAuth を選択してブラウザーで許可します。トークンの貼り付けは不要です。','Добавьте URL сервера в клиент MCP, выберите OAuth и подтвердите доступ в браузере. Вставлять токен не нужно.','Thêm URL máy chủ vào ứng dụng MCP, chọn OAuth và xác nhận trong trình duyệt. Không cần dán token.'],
['MCP tool list','MCP 工具列表','MCP 工具清單','Liste des outils MCP','MCP ツール一覧','Список инструментов MCP','Danh sách công cụ MCP'],
['Compact: metamcp only','精简：仅显示 metamcp','精簡：僅顯示 metamcp','Compact : metamcp seul','簡潔：metamcp のみ','Кратко: только metamcp','Gọn: chỉ metamcp'],
['Full: individual tools too','完整：同时显示独立工具','完整：同時顯示獨立工具','Complet : outils individuels aussi','完全：個別ツールも表示','Полно: также отдельные инструменты','Đầy đủ: thêm từng công cụ'],
['Copy server URL','复制服务器地址','複製伺服器位址','Copier l’URL du serveur','サーバー URL をコピー','Копировать URL сервера','Sao chép URL máy chủ']]
text=''.join('  '+json.dumps(row,ensure_ascii=False)+',\n' for row in rows)
edit('apps/web/src/features/tool-market/provider-i18n.ts','const rows = [','const rows = [\n'+text)
f='apps/web/src/features/debug/console-page-fixtures.ts'
edit(f,'capabilities: { mcp_oauth: true },','''capabilities: { mcp_oauth: true, metamcp: true },
    meta_tool: {
      name: 'metamcp', title: 'LMM tool management and invocation',
      description: 'Management is free. Invocation uses target pricing.',
      inputSchema: { type: 'object', properties: { action: { enum: ['status', 'search', 'details', 'load', 'authorize', 'invoke'] } }, required: ['action'] },
    },''')
f='apps/web/scripts/tool-market-provider-review.mjs'
edit(f,"await page.getByTestId('persona-debug-trigger').waitFor()", """await page.getByTestId('persona-debug-trigger').waitFor()
        await page.getByRole('heading', { name: 'metamcp', exact: true }).waitFor()
        await capture(`metamcp-${width}-${colorScheme}.png`)""")
edit(f,"await capture(`oauth-${width}-${colorScheme}.png`)","""assert.ok((await page.locator('pre').first().innerText()).includes('?mode=compact'))
        await capture(`oauth-${width}-${colorScheme}.png`)
        await page.getByRole('button', { name: '完整：同时显示独立工具', exact: true }).click()
        assert.ok(!(await page.locator('pre').first().innerText()).includes('?mode=compact'))
        await capture(`oauth-full-${width}-${colorScheme}.png`)""")
with (p/'apps/web/src/features/tool-market/oauth-connection.test.ts').open('a') as out:out.write(r'''

test('compact OAuth connection quotes the URL without changing the resource path', () => {
  const command = marketOAuthCommand('https://api.lmm.best/mcp/market?mode=compact')
  assert.ok(command.includes("--url 'https://api.lmm.best/mcp/market?mode=compact'"))
  assert.ok(command.includes('codex mcp login lmm'))
  assert.ok(!command.includes('TOKEN'))
  for (const endpoint of [
    'https://api.lmm.best/mcp/market?mode=compact&mode=full',
    'https://api.lmm.best/mcp/market?mode=compact&token=secret',
    'https://api.lmm.best/mcp/market?mode=unknown',
    'https://api.lmm.best/mcp/market?mode=compact;echo',
    'https://api.lmm.best/mcp/market?mode=compact#fragment',
    'https://api.lmm.best/mcp/market?mode=compact\nwhoami',
  ]) assert.throws(() => marketOAuthCommand(endpoint))
})
''')
with (p/'apps/web/src/features/tool-market/index.test.tsx').open('a') as out:out.write('''

test('metamcp is visible without an installation and opens native OAuth connection setup', async () => {
  stubNavigation([])
  marketAPI.config = async () => ({
    ...pausedConfig,
    capabilities: { ...pausedConfig.capabilities, metamcp: true, mcp_oauth: true },
    meta_tool: { name: 'metamcp', description: 'Gateway tool', inputSchema: { type: 'object', properties: { action: { enum: ['search', 'details', 'load', 'invoke'] } }, required: ['action'] } },
  })
  const { container } = await mount()
  await waitFor(() => container.querySelector('#market-meta-title')?.textContent === 'metamcp')
  assert.ok(container.textContent?.includes('Loading does not grant payment permission.'))
  const details = container.querySelector<HTMLDetailsElement>('details')
  assert.ok(details)
  assert.equal(details.open, false)
  await click(button('Connect MCP client', container))
  await waitFor(() => container.textContent?.includes('Browser login (recommended)') === true)
  assert.ok(container.querySelector('pre')?.textContent?.includes('?mode=compact'))
  await click(button('Full: individual tools too', container))
  assert.ok(!container.querySelector('pre')?.textContent?.includes('?mode=compact'))
  await click(button('Compact: metamcp only', container))
  assert.ok(container.querySelector('pre')?.textContent?.includes('?mode=compact'))
})

test('a missing or disabled meta capability never advertises a phantom gateway tool', async () => {
  stubNavigation([])
  marketAPI.config = async () => ({
    ...pausedConfig,
    capabilities: { ...pausedConfig.capabilities, metamcp: false },
    meta_tool: { name: 'metamcp', description: 'Unavailable', inputSchema: {} },
  })
  const { container } = await mount()
  await waitFor(() => container.querySelector('#market-search') !== null)
  assert.equal(container.querySelector('#market-meta-title'), null)
  assert.equal(findButton('Connect MCP client', container), undefined)
})
''')
