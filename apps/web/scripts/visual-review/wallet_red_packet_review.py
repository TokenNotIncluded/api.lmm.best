# Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later
"""Review the built frontend with synthetic orders; all API traffic stays local.

Start a SPA server for apps/web/dist on port 4185, then run this script.
Requires Python Playwright; CHROME_EXECUTABLE can select an installed Chromium.
"""
import asyncio
import copy
import json
import os
import time
from pathlib import Path
from urllib.parse import urlparse

from playwright.async_api import async_playwright
from settings_fixture import BUNDLE, USER

ORIGIN = 'http://127.0.0.1:4185'
OUTPUT = Path(os.environ.get('WALLET_REVIEW_OUTPUT', '/tmp/wallet-red-packet-review'))


class Fixture:
    def __init__(self):
        self.user = {**copy.deepcopy(USER), 'id': 7, 'quota': 50_000_000, 'used_quota': 0}
        self.records = []
        self.deleted = []
        self.reject_delete = False
        self.stale_entry = False
        self.documents = 0
        self.requests = []
        base = dict(description='', cover_image='', cover_prompt='', draw_mode='random',
                    per_user_limit=1, start_at=0, end_at=0, enabled=True,
                    created_by=7, created_at=0, updated_at=0)
        self.packets = [
            dict(base, id=1, slug='synthetic-live', title='Token-红包', total_items=22, remaining_items=17, claim_count=5),
            dict(base, id=2, slug='synthetic-empty', title='测试', total_items=7, remaining_items=0, claim_count=7),
        ]

    async def route(self, route):
        request = route.request
        url = urlparse(request.url)
        if f'{url.scheme}://{url.netloc}' != ORIGIN:
            return await route.abort()
        if request.resource_type == 'document':
            self.documents += 1
        if url.path == '/index.html' and self.stale_entry:
            return await route.fulfill(content_type='text/html', body='<script src="/static/js/index.synthetic-new-build.js"></script>')
        if not url.path.startswith('/api/'):
            return await route.continue_()
        self.requests.append([request.method, url.path])
        data = []
        if url.path == '/api/user/auth/refresh':
            data = {**copy.deepcopy(BUNDLE), 'user': self.user}
        elif url.path == '/api/user/self':
            data = self.user
        elif url.path == '/api/status':
            data = dict(system_name='LMM Best', quota_per_unit=500_000, price=1, version='synthetic',
                        assistant={'enabled': False}, announcements_enabled=False, registration_enabled=False)
        elif url.path == '/api/setup':
            data = {'status': True}
        elif url.path == '/api/red-packet/admin':
            data = self.packets
        elif url.path == '/api/red-packet/admin/2' and request.method == 'DELETE':
            if self.reject_delete:
                return await route.fulfill(json={'success': False, 'message': 'Synthetic deletion failure'})
            await asyncio.sleep(0.15)
            self.deleted.append(2)
            self.packets = [p for p in self.packets if p['id'] != 2]
            data = None
        elif url.path == '/api/user/topup/self':
            # Deliberately include an unrelated success to exercise exact matching.
            data = {'items': self.records, 'total': len(self.records)}
        elif url.path == '/api/user/topup/info':
            data = dict(enable_online_topup=True, enable_stripe_topup=False,
                        pay_methods=[dict(name='Alipay', type='alipay', settlement_currency='CNY',
                                          platform_units_per_usd='1', settlement_units_per_usd='7')],
                        min_topup=10, stripe_min_topup=10, amount_options=[10, 100, 200, 500], discount={})
        elif url.path == '/api/user/amount':
            return await route.fulfill(json={'message': 'success', 'data': '700.00'})
        elif url.path in ('/api/user/groups', '/api/user/self/groups'):
            data = {'default': {'desc': 'Default', 'ratio': 1}}
        elif url.path == '/api/user/self/onboarding/todo':
            data = {'items': [], 'count': 0}
        elif url.path in ('/api/release-notes/latest', '/api/assistant/handoff/latest'):
            data = None
        elif url.path in ('/api/notice', '/api/user/aff'):
            data = ''
        elif url.path == '/api/assistant/preferences':
            data = {'enabled': False}
        return await route.fulfill(json={'success': True, 'data': data})


def order(trade_no, status='success'):
    now = int(time.time())
    return dict(id=11 if trade_no == 'synthetic-order' else 99, user_id=7, amount=100,
                platform_amount_micros=100_000_000, money=100, trade_no=trade_no,
                payment_method='alipay', create_time=now-3600, complete_time=now, status=status)


async def prepare(browser, fixture, width=1440, reduced=False, receipt=False):
    context = await browser.new_context(viewport={'width': width, 'height': 900 if width > 640 else 844},
                                        locale='zh-CN', reduced_motion='reduce' if reduced else 'no-preference',
                                        service_workers='block')
    await context.route('**/*', fixture.route)
    await context.add_init_script("""localStorage.setItem('i18nextLng','zhCN');
      localStorage.setItem('lmm:source-consent:v2','no');
      localStorage.setItem('newapi:default:cache-version','default-v1');
      document.cookie='vite-ui-theme=dark; path=/';""")
    page = await context.new_page()
    errors = []
    page.on('pageerror', lambda error: errors.append(str(error)))
    if receipt:
        await page.goto(ORIGIN + '/')
        await page.evaluate("""() => localStorage.setItem('wallet-topup-cloud:7', JSON.stringify([{
          userId:7, attemptId:'synthetic-attempt', tradeNo:'synthetic-order',
          beforeQuota:50000000, expectedCredit:100, launchedAt:Date.now()-3600000,
          expiresAt:Date.now()+86400000
        }]))""")
    return context, page, errors


async def capture(page, name):
    await page.evaluate('document.fonts.ready')
    assert await page.evaluate('document.documentElement.scrollWidth <= innerWidth + 1')
    await page.screenshot(path=str(OUTPUT / name), full_page=True)


async def main():
    OUTPUT.mkdir(parents=True, exist_ok=True)
    report = []
    async with async_playwright() as playwright:
        launch = {'headless': True}
        if os.environ.get('CHROME_EXECUTABLE'):
            launch['executable_path'] = os.environ['CHROME_EXECUTABLE']
        browser = await playwright.chromium.launch(**launch)
        try:
            for width in (1440, 390):
                fixture = Fixture()
                context, page, errors = await prepare(browser, fixture, width)
                await page.goto(ORIGIN + '/red-packets')
                empty = page.locator('[data-packet-id="2"]')
                await empty.get_by_text('已领完', exact=True).wait_for()
                assert await page.locator('[data-packet-id="1"]').get_by_role('button', name='删除红包').count() == 0
                await capture(page, f'red-packets-{width}.png')
                await empty.get_by_role('button', name='删除红包').click()
                dialog = page.get_by_role('alertdialog')
                await dialog.get_by_role('button', name='取消', exact=True).click()
                assert fixture.deleted == []
                await empty.get_by_role('button', name='删除红包').click()
                await capture(page, f'red-packets-confirm-{width}.png')
                fixture.reject_delete = True
                await dialog.get_by_role('button', name='删除', exact=True).click()
                await page.get_by_text('Synthetic deletion failure', exact=True).first.wait_for()
                assert await empty.count() == 1
                assert await dialog.is_visible()
                fixture.reject_delete = False
                await dialog.get_by_role('button', name='删除', exact=True).click()
                await empty.wait_for(state='detached')
                assert fixture.deleted == [2]
                assert not errors, errors
                report.append({'scenario': f'red-packet-delete-{width}', 'passed': True})
                await context.close()

            fixture = Fixture()
            fixture.records = [order('unrelated-success'), order('synthetic-order', 'pending')]
            context, page, errors = await prepare(browser, fixture, width=390, receipt=True)
            await page.goto(ORIGIN + '/wallet')
            cloud = page.get_by_test_id('wallet-token-cloud-balance')
            await cloud.wait_for()
            await page.wait_for_timeout(500)
            box = await cloud.bounding_box()
            assert box and box['y'] >= 0 and box['y'] + box['height'] <= 844, box
            assert await cloud.get_attribute('data-success') is None
            await capture(page, 'wallet-mobile-first-viewport.png')
            assert await page.evaluate("localStorage.getItem('wallet-topup-cloud:7') !== null")
            # An independent return tab must retain a one-hour-old checkout.
            await page.close()
            fixture.records = [order('synthetic-order', 'pending')]
            page = await context.new_page()
            page.on('pageerror', lambda error: errors.append(str(error)))
            await page.goto(ORIGIN + '/wallet')
            cloud = page.get_by_test_id('wallet-token-cloud-balance')
            await cloud.wait_for()
            content = page.locator('.console-section-content')
            await content.evaluate('(el) => { el.scrollTop = el.scrollHeight }')
            await page.wait_for_timeout(200)
            fixture.records = [order('synthetic-order')]
            fixture.user['quota'] = 60_000_000  # 100 + 100 - 80 consumed = 120.
            await page.evaluate("window.dispatchEvent(new Event('focus'))")
            await page.wait_for_timeout(3400)
            assert await cloud.get_attribute('data-success') is None
            assert await page.evaluate("localStorage.getItem('wallet-topup-cloud:7') !== null")
            await cloud.scroll_into_view_if_needed()
            await page.wait_for_function("document.querySelector('[data-testid=\"wallet-token-cloud-balance\"]')?.dataset.success === 'true'")
            await page.wait_for_timeout(350)
            await capture(page, 'wallet-mobile-success.png')
            await page.wait_for_function("localStorage.getItem('wallet-topup-cloud:7') === null")
            assert await cloud.get_attribute('data-success') is None
            await page.reload()
            await page.get_by_test_id('wallet-token-cloud-balance').wait_for()
            assert await page.get_by_test_id('wallet-token-cloud-balance').get_attribute('data-success') is None
            assert not errors, errors
            report.append({'scenario': 'late-return-exact-order-offscreen-receipt', 'passed': True})
            await context.close()

            fixture = Fixture()
            fixture.records = [order('synthetic-order')]
            fixture.user['quota'] = 60_000_000
            context, page, errors = await prepare(browser, fixture, width=390, reduced=True, receipt=True)
            await page.goto(ORIGIN + '/wallet')
            await page.get_by_test_id('wallet-token-cloud-balance').wait_for()
            await page.wait_for_function("localStorage.getItem('wallet-topup-cloud:7') === null")
            assert await page.get_by_test_id('wallet-token-cloud-balance').get_attribute('data-success') is None
            await capture(page, 'wallet-reduced-motion.png')
            assert not errors, errors
            report.append({'scenario': 'reduced-motion-static-completion', 'passed': True})
            await context.close()

            fixture = Fixture()
            fixture.stale_entry = True
            context, page, errors = await prepare(browser, fixture)
            await page.goto(ORIGIN + '/wallet')
            refresh = page.get_by_role('button', name='刷新页面', exact=True)
            await refresh.wait_for()
            await page.wait_for_timeout(700)
            assert fixture.documents == 1, 'a stale entry must not force navigation'
            await capture(page, 'wallet-update-notice.png')
            assert not errors, errors
            report.append({'scenario': 'manual-update-notice-no-forced-refresh', 'passed': True})
            await context.close()
        finally:
            (OUTPUT / 'results.json').write_text(json.dumps(report, indent=2))
            await browser.close()
    print(json.dumps(report, indent=2))


if __name__ == '__main__':
    asyncio.run(main())
