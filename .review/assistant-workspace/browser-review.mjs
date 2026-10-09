import assert from 'node:assert/strict'
import { spawn } from 'node:child_process'
import fs from 'node:fs/promises'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { chromium } from '@playwright/test'

const cwd = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const out = path.join(cwd, 'artifacts/assistant-workspace-browser')
await fs.mkdir(out, {recursive:true})
const origin = 'http://127.0.0.1:5173'
const server = spawn('bun', ['run','dev','--host','127.0.0.1','--port','5173','--strict-port'], {cwd,env:{...process.env,RSBUILD_APP_ENTRY:'./src/debug-main.tsx',RSBUILD_PERSONA_DEBUG:'true',RSBUILD_API_PROXY:'http://127.0.0.1:9'},stdio:['ignore','pipe','pipe']})
let log = ''
server.stdout.on('data',(value)=>{log+=value})
server.stderr.on('data',(value)=>{log+=value})
const sleep=(ms)=>new Promise(resolve=>setTimeout(resolve,ms))
const reports=[]
let browser
let activePage
try {
  let ready=false
  for(let n=0;n<120;n++) { try { const res=await fetch(origin); if(res.ok){ready=true;break} }catch{} await sleep(1000) }
  assert.ok(ready,'Rsbuild did not start')
  browser=await chromium.launch({headless:true})
  for(const width of [1440,390]) for(const theme of ['light','dark']) {
    const context=await browser.newContext({viewport:{width,height:width===390?844:1100},colorScheme:theme,locale:'zh-CN',reducedMotion:'reduce'})
    await context.addInitScript(({theme})=>{
      document.cookie=`theme=${theme};path=/`
      document.cookie='lang=zh;path=/'
      localStorage.setItem('theme',theme)
      localStorage.setItem('i18nextLng','zh')
      localStorage.setItem('lang','zh')
      localStorage.setItem('sidebar_state','false')
      localStorage.setItem('lmm-onboarding-tour-dismissed:1005','true')
    },{theme})
    const page=await context.newPage()
    activePage=page
    const errors=[], external=[]
    page.on('pageerror',error=>errors.push(String(error)))
    await page.route('**/*', async route=>{
      const url=new URL(route.request().url())
      if(url.origin===origin || ['data:','blob:'].includes(url.protocol)) return route.continue()
      external.push(url.origin+url.pathname)
      return route.abort()
    })
    const tag=`${width}-${theme}`
    async function capture(name,locator) {
      await sleep(200)
      const metrics=await page.evaluate(()=>({width:innerWidth,scroll:document.documentElement.scrollWidth,dark:document.documentElement.classList.contains('dark')}))
      assert.ok(metrics.scroll<=metrics.width+1,`Overflow ${name}: ${JSON.stringify(metrics)}`)
      assert.equal(metrics.dark,theme==='dark',`Wrong actual theme: ${name}`)
      await (locator??page).screenshot({path:path.join(out,`${tag}-${name}.png`),animations:'disabled'})
      reports.push({tag,name,...metrics})
    }
    await page.goto(`${origin}/system-settings/content/assistant?debug_persona=admin&console_review=1`,{waitUntil:'domcontentloaded'})
    await page.locator('[data-settings-tab="tools"]').click({timeout:60000})
    await page.locator('[data-tool-name="search_web"]').waitFor()
    assert.equal(await page.locator('[data-tool-name]').count(),67)
    await capture('tool-center')
    for(const tool of ['search_web','prepare_new_user_gift','prepare_weekly_discount','call_market_tool']) {
      await page.locator(`[data-tool-name="${tool}"] .assistant-tool-row-actions button`).first().click()
      const dialog=page.locator('[data-testid="assistant-tool-configuration"]')
      await dialog.waitFor()
      if(tool==='search_web') assert.equal(await dialog.locator('input[name="AssistantSearchAPIKey"]').count(),1)
      if(tool==='prepare_new_user_gift') assert.equal(await dialog.locator('input[name="AssistantNewUserGiftMaxCredits"]').count(),1)
      if(tool==='prepare_weekly_discount') {
        const limits=dialog.locator('input[type="number"]')
        assert.equal(await limits.count(),7)
        await limits.nth(1).fill('25')
        assert.equal(await limits.nth(5).isDisabled(),true)
      }
      if(tool==='call_market_tool') await dialog.getByText('文档检索 · 演示服务',{exact:true}).waitFor()
      await capture(tool,dialog)
      await page.keyboard.press('Escape')
      await dialog.waitFor({state:'detached'})
    }
    assert.deepEqual(await page.evaluate(()=>window.workspaceReviewWrites),[],'Configuration drafts must not save themselves')
    await page.goto(`${origin}/desktop?debug_persona=l1&console_review=1`,{waitUntil:'domcontentloaded'})
    await page.locator('.overview-greeting').waitFor({timeout:60000})
    await page.locator('.overview-chart-grid .assistant-visual').first().waitFor()
    await capture('overview')
    await capture('usage-charts',page.locator('.overview-usage-charts'))
    await page.locator('.overview-greeting-meta button').click()
    const greeting=page.locator('[role="dialog"] textarea')
    await greeting.fill('HI,$name,现在是$time · $level')
    await capture('greeting-editor',page.locator('[role="dialog"]'))
    await page.keyboard.press('Escape')
    await page.locator('[data-testid="assistant-launcher"]').click()
    const textarea=page.locator('#ai-assistant-panel textarea').last()
    await textarea.waitFor()
    await textarea.fill('用图表显示演示数据，并提供下一步选项。')
    await page.locator('#ai-assistant-panel [data-testid="assistant-prompt-form"] button[type="submit"]').click()
    const cards=page.locator('#ai-assistant-panel .assistant-visual')
    await cards.first().waitFor({timeout:30000})
    assert.equal(await cards.count(),5)
    await capture('chat-statistics',cards.nth(0))
    await capture('chat-trend',cards.nth(1))
    await capture('chat-flow',cards.nth(3))
    const calls=await page.evaluate(()=>window.workspaceReviewChatCalls())
    await cards.nth(4).locator('button').first().click()
    assert.equal(await textarea.inputValue(),'请查看我的用量')
    assert.equal(await page.evaluate(()=>window.workspaceReviewChatCalls()),calls,'Choice must not submit a chat turn')
    assert.deepEqual(errors,[])
    reports.push({tag,externalBlocked:external,errors,chatRequests:calls,choiceOnlyFillsComposer:true})
    await fs.writeFile(path.join(out,'report.json'),JSON.stringify(reports,null,2))
    await context.close()
  }
} catch(error) {
  if(activePage && !activePage.isClosed()) {
    await activePage.screenshot({path:path.join(out,'failure.png')}).catch(()=>{})
    await fs.writeFile(path.join(out,'failure.txt'),String(error)+'\n'+await activePage.locator('body').innerText().catch(()=>''))
  }
  throw error
} finally {
  await fs.writeFile(path.join(out,'server.log'),log)
  await fs.writeFile(path.join(out,'report.json'),JSON.stringify(reports,null,2))
  await browser?.close()
  server.kill('SIGTERM')
}
