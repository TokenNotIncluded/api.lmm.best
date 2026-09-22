# Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later
"""Exercise real built routes with synthetic accounts; never contacts production."""
import asyncio
import copy
import functools
import json
import os
import threading
from http.server import SimpleHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from urllib.parse import urlparse
from playwright.async_api import async_playwright
from settings_fixture import BUNDLE, Fixture

WEB = Path(__file__).resolve().parents[2]
OUT = Path(os.environ.get('ASSISTANT_REVIEW_OUTPUT', WEB.parents[1] / 'output/assistant-review'))
OUT.mkdir(parents=True, exist_ok=True)
ANSWER = '先选择你常用的客户端，再完成连接。\n\n告诉我你的设备和使用场景，我会带你完成第一次调用。'

class Handler(SimpleHTTPRequestHandler):
    def do_GET(self):
        if not Path(self.translate_path(urlparse(self.path).path)).is_file():
            self.path = '/index.html'
        super().do_GET()
    def log_message(self, *_):
        pass

class AssistantFixture(Fixture):
    def __init__(self, activated=False):
        super().__init__()
        self.activated = activated
        self.chats = []
        self.bundle = copy.deepcopy(BUNDLE)
        self.user = self.bundle['user']
        self.user.update(id=7101 if activated else 7100, role=1,
                         username='preview_user', display_name='预览用户',
                         developer_access_granted=activated)
        self.user['trust_level_info'].update(level=1 if activated else 0, automatic_level=1 if activated else 0)
        self.user['onboarding'].update(activation_complete=activated, stage='complete' if activated else 'activate',
                                       paid_activation_enabled=True, paid_activation_min_amount=1)

    async def route(self, route):
        req = route.request
        path = urlparse(req.url).path
        data = None
        if path == '/api/assistant/chat':
            self.requests.append([req.method, path])
            self.chats.append(req.post_data_json)
            return await route.fulfill(content_type='application/json', body=json.dumps({
                'choices': [{'message': {'role': 'assistant', 'content': ANSWER}}],
                'lmm_assistant_history': {'conversation_id': 7201}}, ensure_ascii=False))
        if path == '/api/user/auth/refresh':
            data = self.bundle
        elif path == '/api/user/self':
            data = self.user
        elif path == '/api/assistant/status':
            data = {'enabled': True, 'model': 'fixture-model', 'route_available': True,
                    'developer_access_granted': self.activated, 'is_admin': False,
                    'access_level': 'L1' if self.activated else 'L0',
                    'funding': {'mode': 'super_administrator'}}
        elif path == '/api/assistant/pre-conversation-presets':
            data = {'presets': [{'id': 'getting_started', 'label': '第一次使用', 'prompt': '带我完成第一次模型调用'},
                                {'id': 'client_setup', 'label': '连接客户端', 'prompt': '帮助我配置客户端连接'},
                                {'id': 'plan_question', 'label': '了解套餐', 'prompt': '解释一下套餐和用量'}]}
        elif path == '/api/assistant/support/eligibility':
            data = {'eligible': False}
        elif path == '/api/assistant/support/self':
            data = {'request': None}
        elif path == '/api/assistant/registration-check':
            data = {'state': 'active' if self.activated else 'ready'}
        elif path == '/api/user/self/onboarding/todo':
            data = {'eligibility': {'eligible': False}, 'status': 'completed', 'steps': []}
        elif path == '/api/user/developer-access/request':
            data = {'status': 'pending'}
        elif path == '/api/assistant/new-user-gift':
            data = {'status': 'claimed', 'amount_cents': 300, 'reason': '已用于体验平台模型与客户端连接。'}
        elif path == '/api/assistant/weekly-discount':
            data = None
        elif path == '/api/assistant/conversations':
            data = {'conversations': [{'id': 7201, 'title': '客户端连接', 'last_message_preview': ANSWER, 'created_at': 1790100000, 'updated_at': 1790100000, 'archived_at': 0, 'owner': 'self', 'privacy_notice': ''}] if self.chats else []}
        elif path == '/api/assistant/offers':
            data = {'ok': True, 'plans': [], 'topup_discounts': [], 'price': 1}
        elif path == '/api/user/models':
            data = ['fixture-model']
        elif '/api/assistant/pre-conversation-presets/' in path:
            data = {}
        else:
            return await super().route(route)
        self.requests.append([req.method, path])
        return await route.fulfill(content_type='application/json', body=json.dumps(
            {'success': True, 'data': data, 'message': ''}, ensure_ascii=False))

async def main():
    server = ThreadingHTTPServer(('127.0.0.1', 0), functools.partial(Handler, directory=str(WEB / 'dist')))
    threading.Thread(target=server.serve_forever, daemon=True).start()
    base = f'http://127.0.0.1:{server.server_port}'
    report = {'fixture': 'Synthetic API/account fixtures, actual built application. No production access.',
              'checks': [], 'errors': [], 'pages': {}}
    async with async_playwright() as p:
        browser = await p.chromium.launch()
        async def scenario(name, activated, width, height, dark=False):
            context = await browser.new_context(viewport={'width': width, 'height': height},
                locale='zh-CN', color_scheme='dark' if dark else 'light',
                reduced_motion='reduce', record_video_dir=str(OUT / 'video'),
                record_video_size={'width': width, 'height': height})
            fixture = AssistantFixture(activated)
            async def route_all(route):
                if urlparse(route.request.url).hostname not in ('127.0.0.1', 'localhost'):
                    return await route.abort()
                if '/api/' in route.request.url:
                    return await fixture.route(route)
                return await route.continue_()
            await context.route('**/*', route_all)
            await context.add_init_script("localStorage.setItem('i18nextLng','zhCN'); document.cookie='vite-ui-theme=%s; path=/';" % ('dark' if dark else 'light'))
            page = await context.new_page()
            page.on('pageerror', lambda error: report['errors'].append({'scenario': name, 'error': str(error)}))
            async def capture(suffix):
                await page.screenshot(path=str(OUT / f'{name}-{suffix}.png'))
                report['pages'][name + '-' + suffix] = {
                    'body': (await page.locator('body').inner_text())[:6000],
                    'overflow': await page.evaluate('document.documentElement.scrollWidth > innerWidth'),
                    'viewport': page.viewport_size}
            try:
                await page.goto(base + '/getting-started')
                await page.evaluate('document.fonts.ready')
                if not activated:
                    await page.locator('#l0-question').wait_for(timeout=25000)
                    await page.wait_for_timeout(800)
                    assert await page.get_by_test_id('assistant-rail').count() == 0
                    assert await page.get_by_test_id('assistant-mobile-launcher').count() == 0
                    assert await page.locator('#ai-assistant-panel').count() == 0
                    assert await page.locator('button[aria-label="打开 AI 助手"]').count() == 0
                    await capture('single-chat')
                    # One request only, and no hidden global handler consumes it.
                    await page.locator('#l0-question').fill('如何开始使用平台？')
                    await page.locator('#l0-question').press('Enter')
                    await page.locator('.l0-answer').filter(has_text='先选择').wait_for()
                    await page.wait_for_timeout(1000)
                    assert len(fixture.chats) == 1
                    await capture('conversation')
                    # Explicit help replaces cloud, retaining the same mounted draft/history.
                    await page.locator('.l0-help-menu summary').click()
                    await page.locator('.l0-help-content button').nth(1).click()
                    await page.get_by_test_id('l0-assistant-task').wait_for()
                    assert await page.locator('.l0-help-menu').get_attribute('open') is None
                    await page.locator('#ai-assistant-panel textarea').wait_for()
                    assert await page.locator('#l0-question').is_hidden()
                    assert await page.get_by_test_id('assistant-rail').count() == 0
                    assert len(fixture.chats) == 1, 'Opening a tool must not send a duplicate prompt'
                    await capture('inline-task')
                    await page.get_by_role('button', name='返回对话', exact=True).click()
                    assert await page.locator('#ai-assistant-panel').count() == 0
                    assert await page.locator('.l0-answer').filter(has_text='先选择').is_visible()
                    await page.keyboard.press('Control+Shift+A')
                    await page.wait_for_function("document.activeElement?.id === 'l0-question'")
                    await capture('returned')
                else:
                    if width >= 1280:
                        await page.locator('button[aria-label="打开 AI 助手"]').click(timeout=25000)
                    else:
                        # Header button is only visible above 640px; the launcher is intentionally
                        # hidden on getting-started. Test narrow overlay from another real console route.
                        await page.goto(base + '/profile')
                        await page.get_by_test_id('assistant-launcher').click(timeout=25000)
                    panel = page.locator('#ai-assistant-panel')
                    await panel.locator('textarea').wait_for()
                    await page.wait_for_timeout(800)
                    if width < 640:
                        box = await panel.bounding_box()
                        assert abs(box['width'] - width) <= 1, box
                        assert abs(box['height'] - height) <= 1, box
                    await capture('empty')
                    if width >= 1280:
                        box = await panel.bounding_box()
                        assert box['height'] <= 770, box
                    await panel.locator('textarea').fill('如何连接我的客户端？')
                    await panel.locator('textarea').press('Enter')
                    await panel.get_by_text('先选择你常用的客户端，再完成连接。', exact=False).first.wait_for()
                    await page.wait_for_timeout(500)
                    assert len(fixture.chats) == 1
                    await capture('answer')
                    assert await panel.get_by_test_id('assistant-composer-footer').is_visible()
                    assert await panel.get_by_test_id('assistant-preset-prompts').count() == 0
                    assert await panel.get_by_test_id('assistant-retry-last').count() == 0
                    await panel.get_by_test_id('assistant-history-toggle').click()
                    await page.get_by_test_id('assistant-history-list').wait_for()
                    await capture('history')
                    await panel.get_by_test_id('assistant-history-toggle').click()
                    await panel.locator('textarea').wait_for()
                    if width >= 1280:
                        await page.get_by_test_id('assistant-collapse').click()
                        assert await page.get_by_test_id('assistant-rail').get_attribute('inert') is not None
                        await page.locator('button[aria-label="打开 AI 助手"]').click()
                        assert await panel.get_by_text('先选择你常用的客户端，再完成连接。', exact=False).first.is_visible()
                    else:
                        await page.get_by_test_id('assistant-close').click()
                        await panel.wait_for(state='hidden')
                assert not any(v['overflow'] for k,v in report['pages'].items() if k.startswith(name+'-'))
                report['checks'].append({'name': name, 'passed': True, 'chatRequests': len(fixture.chats)})
            except Exception as error:
                report['checks'].append({'name': name, 'passed': False, 'error': str(error)})
                await capture('failure')
            finally:
                await context.close()
        for args in [
            ('l0-desktop-dark', False, 1440, 960, True),
            ('l0-mobile', False, 390, 844, False),
            ('l1-desktop-dark', True, 1920, 1440, True),
            ('l1-desktop-light', True, 1440, 900, False),
            ('l1-mobile', True, 390, 844, False),
            ('l1-small-mobile', True, 320, 568, True),
        ]:
            await scenario(*args)
        await browser.close()
    server.shutdown()
    (OUT / 'report.json').write_text(json.dumps(report, ensure_ascii=False, indent=2))
    print(json.dumps({'checks': report['checks'], 'errors': report['errors']}, ensure_ascii=False, indent=2))
    if report['errors'] or any(not c['passed'] for c in report['checks']):
        raise SystemExit(1)

if __name__ == '__main__':
    asyncio.run(main())
