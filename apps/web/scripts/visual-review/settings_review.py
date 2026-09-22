# Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later
"""Record the built application with isolated, synthetic API fixtures.

pip install playwright==1.58.0
python -m playwright install chromium
bun run build:web
python apps/web/scripts/visual-review/settings_review.py

No production account, credentials or mutations are used.
"""
import asyncio
import functools
import json
import os
import re
import threading
from http.server import SimpleHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from urllib.parse import urlparse

from playwright.async_api import async_playwright
from settings_fixture import Fixture, setup

WEB = Path(__file__).resolve().parents[2]
OUT = Path(os.environ.get('SETTINGS_REVIEW_OUTPUT', WEB.parents[1] / 'output/settings-review'))
OUT.mkdir(parents=True, exist_ok=True)

class Handler(SimpleHTTPRequestHandler):
    def do_GET(self):
        if not Path(self.translate_path(urlparse(self.path).path)).is_file():
            self.path = '/index.html'
        super().do_GET()
    def log_message(self, *_):
        pass

ROUTES = {
    'site': '/system-settings/site/system-info',
    'auth': '/system-settings/auth/basic-auth',
    'billing': '/system-settings/billing/quota',
    'models': '/system-settings/models/global',
    'security': '/system-settings/security/rate-limit',
    'assistant': '/system-settings/content/assistant',
    'operations': '/system-settings/operations/email',
}

async def main():
    server = ThreadingHTTPServer(('127.0.0.1', 0), functools.partial(Handler, directory=str(WEB / 'dist')))
    threading.Thread(target=server.serve_forever, daemon=True).start()
    url = f'http://127.0.0.1:{server.server_port}'
    report = {'fixture': 'Synthetic, isolated API data. Production not contacted.', 'checks': [], 'errors': [], 'pages': {}}
    async with async_playwright() as p:
        browser = await p.chromium.launch()
        context = await browser.new_context(viewport={'width': 1440, 'height': 1000}, locale='zh-CN', device_scale_factor=1,
                                            record_video_dir=str(OUT / 'raw-video'), record_video_size={'width': 1440, 'height': 1000})
        fixture = Fixture()
        await setup(context, fixture)
        page = await context.new_page()
        page.on('pageerror', lambda error: report['errors'].append(str(error)))

        async def capture(name):
            await page.screenshot(path=str(OUT / f'{name}.png'))
            report['pages'][name] = {'url': page.url, 'body': (await page.locator('body').inner_text())[:12000],
                                   'viewport': page.viewport_size,
                                   'overflow': await page.evaluate('document.documentElement.scrollWidth > innerWidth'),
                                   'headingFont': await page.locator('.console-section-header :is(h1, h2)').first.evaluate('e => getComputedStyle(e).fontFamily'),
                                   'inputs': await page.locator('.settings-sheet input').evaluate_all('es => es.slice(0, 5).map(e => ({slot: e.dataset.slot, height: e.getBoundingClientRect().height, minHeight: getComputedStyle(e).minHeight}))')}

        async def check(name, fn):
            try:
                await fn()
                report['checks'].append({'name': name, 'passed': True})
            except Exception as error:
                report['checks'].append({'name': name, 'passed': False, 'error': str(error)})
                await capture('failure-' + name)

        async def go(route):
            await page.goto(url + route)
            await page.locator('.settings-sheet').wait_for(timeout=20000)
            await page.evaluate('document.fonts.ready')
            await page.wait_for_timeout(1100)

        async def click(locator):
            await locator.scroll_into_view_if_needed()
            box = await locator.bounding_box()
            if box:
                await page.mouse.move(box['x'] + box['width']/2, box['y'] + box['height']/2, steps=20)
            await locator.click()
            await page.wait_for_timeout(500)

        # Initial visual sweep covers one real page in every settings category.
        for name, route in ROUTES.items():
            async def run(route=route, name=name):
                await go(route)
                await capture('desktop-' + name)
                assert not report['pages']['desktop-' + name]['overflow'], 'Horizontal overflow'
                assert await page.locator('.settings-sheet input, .settings-sheet button').count() > 0, 'Missing settings controls'
            await check('page-' + name, run)

        async def site_flow():
            await go(ROUTES['site'])
            save = page.locator('.settings-form-actions').get_by_role('button').last
            assert await save.is_visible(), 'Save dock is not visible'
            assert await save.is_disabled(), 'Unchanged form can be submitted'
            title = page.get_by_label('系统名称', exact=True)
            await title.fill('LMM Studio')
            await page.wait_for_timeout(600)
            assert await page.locator('.settings-brand-preview').get_by_text('LMM Studio').is_visible()
            await click(save)
            await page.wait_for_timeout(600)
            assert fixture.options['SystemName'] == 'LMM Studio'
            assert await save.is_disabled(), 'Dirty state not cleared after save'
            await click(page.locator('summary').filter(has_text='页面内容').first)
            await capture('site-content-expanded')
            await click(page.locator('summary').filter(has_text='协议与隐私').first)
            await page.get_by_label('用户协议', exact=True).first.scroll_into_view_if_needed()
            assert await save.is_visible(), 'Save dock scrolled away'
            await capture('site-legal-expanded')
        await check('site-edit-save', site_flow)

        async def search_flow():
            await click(page.get_by_role('button', name='搜索设置', exact=True))
            search = page.get_by_placeholder('搜索设置')
            await search.fill('assistant')
            await page.wait_for_timeout(700)
            await capture('settings-search')
            await search.press('ArrowDown')
            await search.press('Enter')
            await page.wait_for_url('**/system-settings/content/assistant')
            await page.wait_for_timeout(900)
            await capture('assistant-after-search')
        await check('search-keyboard-navigation', search_flow)

        async def assistant_flow():
            await go(ROUTES['assistant'])
            assert ['GET', '/api/assistant/models'] in fixture.requests, 'Automatic model list missing'
            before_refresh = fixture.requests.count(['GET', '/api/assistant/models'])
            await click(page.get_by_test_id('assistant-get-model-list'))
            await page.wait_for_timeout(600)
            assert fixture.requests.count(['GET', '/api/assistant/models']) > before_refresh
            await click(page.locator('[data-settings-tab=conversation]'))
            assert await page.locator('#assistant-panel-model').is_hidden()
            assert await page.locator('#assistant-panel-conversation').is_visible()
            await click(page.locator('summary').filter(has_text='开始对话前的快捷提问').first)
            await capture('assistant-starters')
            await click(page.locator('.settings-starter summary').filter(has_text='多语言文案').first)
            await capture('assistant-translations')
            # Edits must survive hiding the panel.
            editor = page.get_by_test_id('assistant-conversation-starters-editor')
            field = editor.get_by_role('textbox', name='按钮文案', exact=True).first
            if await field.count() == 0:
                field = editor.get_by_role('textbox', name='Button label', exact=True).first
            await field.fill('带我完成第一次调用')
            await click(page.locator('[data-settings-tab=model]'))
            await click(page.locator('[data-settings-tab=conversation]'))
            assert await field.input_value() == '带我完成第一次调用'
            await click(page.locator('summary').filter(has_text='开始对话前的快捷提问').first)
            await click(page.locator('summary').filter(has_text='开始对话前的快捷提问').first)
            assert await field.input_value() == '带我完成第一次调用'
            await click(page.locator('.settings-form-actions').get_by_role('button').last)
            await page.wait_for_timeout(600)
            presets = json.loads(fixture.options['AssistantPreConversationPresets'])
            assert presets[0]['label']['default'] == '带我完成第一次调用'
            await capture('assistant-saved')
        await check('assistant-persistent-edit', assistant_flow)

        async def dark_flow():
            await click(page.get_by_role('button', name='打开主题设置', exact=True))
            await click(page.get_by_role('radio', name='选择深色', exact=True))
            await capture('theme-drawer')
            await page.keyboard.press('Escape')
            await page.wait_for_timeout(600)
            assert await page.locator('html').evaluate("e => e.classList.contains('dark')")
            await page.locator('.console-section-content').evaluate('e => e.scrollTo({top: 0, behavior: "smooth"})')
            await page.wait_for_timeout(700)
            await capture('desktop-dark-assistant')
        await check('dark-appearance', dark_flow)

        await context.close()
        if page.video:
            await page.video.save_as(str(OUT / 'settings-desktop.webm'))

        # Compact layouts and reduced motion are separate contexts, not scaled desktop screenshots.
        for width, name in [(390, 'mobile'), (768, 'tablet')]:
            compact = await browser.new_context(viewport={'width': width, 'height': 844}, locale='zh-CN', reduced_motion='reduce', **({'record_video_dir': str(OUT / 'raw-mobile'), 'record_video_size': {'width': 390, 'height': 844}} if width == 390 else {}))
            f = Fixture(); await setup(compact, f)
            q = await compact.new_page()
            q.on('pageerror', lambda error: report['errors'].append(str(error)))
            for section in ['site', 'assistant']:
                try:
                    await q.goto(url + ROUTES[section]); await q.locator('.settings-sheet').wait_for(timeout=20000); await q.evaluate('document.fonts.ready'); await q.wait_for_timeout(1200)
                    await q.screenshot(path=str(OUT / f'{name}-{section}.png'))
                    overflow = await q.evaluate('document.documentElement.scrollWidth > innerWidth')
                    assert not overflow
                    dock = q.locator('.settings-form-actions').last
                    assert await dock.is_visible()
                    if width == 390 and section == 'site':
                        await q.get_by_label('系统名称', exact=True).fill('LMM Studio')
                        await q.wait_for_timeout(800)
                        await dock.get_by_role('button').last.click()
                        await q.wait_for_timeout(800)
                        assert f.options['SystemName'] == 'LMM Studio'
                        await q.locator('summary').filter(has_text='页面内容').first.click()
                        await q.wait_for_timeout(1000)
                        await q.screenshot(path=str(OUT / 'mobile-content-expanded.png'))
                    report['checks'].append({'name': f'{name}-{section}', 'passed': True})
                except Exception as error:
                    report['checks'].append({'name': f'{name}-{section}', 'passed': False, 'error': str(error)})
            await compact.close()
            if width == 390 and q.video:
                await q.video.save_as(str(OUT / 'settings-mobile.webm'))

        # Failed settings loads must not expose an editable default configuration.
        bad = await browser.new_context(viewport={'width': 1440, 'height': 1000}, locale='zh-CN')
        f = Fixture(); f.fail_options = True; await setup(bad, f)
        q = await bad.new_page()
        try:
            await q.goto(url + ROUTES['site']); await q.get_by_text(re.compile(r'^(无法加载设置|Unable to load settings)$')).wait_for(timeout=45000)
            assert await q.locator('.settings-sheet input').count() == 0
            await q.screenshot(path=str(OUT / 'load-error.png'))
            assert await q.locator('.settings-save-dock').is_visible() is False
            f.fail_options = False
            await q.get_by_role('button', name=re.compile(r'^(重试|Retry)$')).click()
            await q.get_by_label('系统名称', exact=True).wait_for(timeout=20000)
            assert await q.get_by_label('系统名称', exact=True).input_value() == 'LMM'
            report['checks'].append({'name': 'failed-load-no-default-form-and-retry', 'passed': True})
        except Exception as error:
            await q.screenshot(path=str(OUT / 'failure-load-error.png'))
            report['pages']['failure-load-error'] = {'body': await q.locator('body').inner_text(), 'url': q.url, 'requests': f.requests}
            report['checks'].append({'name': 'failed-load-no-default-form', 'passed': False, 'error': str(error)})
        await bad.close()
        await browser.close()
    server.shutdown()
    (OUT / 'review.json').write_text(json.dumps(report, ensure_ascii=False, indent=2))
    print(json.dumps({'checks': report['checks'], 'errors': report['errors']}, ensure_ascii=False, indent=2))
    if report['errors'] or any(not c['passed'] for c in report['checks']):
        raise SystemExit(1)

if __name__ == '__main__':
    asyncio.run(main())
