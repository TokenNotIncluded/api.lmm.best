from pathlib import Path
import json

changed = []
def edit(path, old, new, count=1):
    p = Path(path)
    text = p.read_text()
    actual = text.count(old)
    if actual != count:
        raise RuntimeError(f'{path}: expected {count} matches, got {actual}')
    p.write_text(text.replace(old, new))
    if path not in changed: changed.append(path)
def add(path, text):
    p = Path(path)
    if p.exists(): raise RuntimeError(f'File already exists: {path}')
    p.parent.mkdir(parents=True, exist_ok=True)
    p.write_text(text)
    changed.append(path)
def append(path, text):
    p = Path(path)
    p.write_text(p.read_text() + text)
    if path not in changed: changed.append(path)

add('apps/api-go/service/merchant_store_extore_crypto.go', '''package service

import (
    "os"
    "strings"
    "github.com/LIghtJUNction/api.lmm.best/common"
)

const MerchantStoreExtorePurpose = "store_extore_import"
const merchantExtoreEnvelope = "merchant:"

func EncryptMerchantStoreExtoreFlow(value string) (string, error) {
    if strings.TrimSpace(os.Getenv("MERCHANT_STORE_ENCRYPTION_KEY")) != "" {
        encrypted, err := common.EncryptPersistentString(MerchantStoreExtorePurpose, "MERCHANT_STORE_ENCRYPTION_KEY", "", value)
        if err != nil { return "", err }
        return merchantExtoreEnvelope + encrypted, nil
    }
    return common.EncryptPersistentString(MerchantStoreExtorePurpose, "CRYPTO_SECRET", "SESSION_SECRET", value)
}

func DecryptMerchantStoreExtoreFlow(value string) (string, error) {
    if strings.HasPrefix(value, merchantExtoreEnvelope) {
        return common.DecryptPersistentString(MerchantStoreExtorePurpose, "MERCHANT_STORE_ENCRYPTION_KEY", "", strings.TrimPrefix(value, merchantExtoreEnvelope))
    }
    // Existing v1 flows use the original CRYPTO_SECRET / SESSION_SECRET choice.
    return common.DecryptPersistentString(MerchantStoreExtorePurpose, "CRYPTO_SECRET", "SESSION_SECRET", value)
}
''')
p = 'apps/api-go/controller/merchant_store_extore.go'
edit(p, 'const storeExtorePurpose = "store_extore_import"', 'const storeExtorePurpose = service.MerchantStoreExtorePurpose')
edit(p, 'common.EncryptPersistentString(storeExtorePurpose, "CRYPTO_SECRET", "SESSION_SECRET", string(raw))', 'service.EncryptMerchantStoreExtoreFlow(string(raw))')
edit(p, 'common.DecryptPersistentString(storeExtorePurpose, "CRYPTO_SECRET", "SESSION_SECRET", pending.Payload)', 'service.DecryptMerchantStoreExtoreFlow(pending.Payload)')
message = 'Extore import needs a persistent server encryption key. Ask an administrator to configure it.'
edit(p, '\t\tmerchantStoreRespond(c, nil, err)\n\t\treturn\n', '\t\tcode, message, status = "STORE_EXTORE_SECURE_STORAGE", "' + message + '", http.StatusServiceUnavailable\n')
discover = '''\tif err = service.MerchantStoreExtoreClient.Discover(c.Request.Context(), flow.Origin); err != nil {
\t\tstoreExtoreError(c, err)
\t\treturn
\t}
'''
edit(p, discover, '')
edit(p, '\tstate, _, err := model.CreateAuthFlow(', discover + '\tstate, _, err := model.CreateAuthFlow(')
p = 'apps/api-go/internal/extore/client.go'
edit(p, '&catalog, 4<<20', '&catalog, 16<<20')
edit(p, 'len(listing.Variants) < 1 || len(listing.Variants) > 200', 'listing.Variants == nil || len(listing.Variants) > 100')
add('apps/api-go/service/merchant_store_extore_crypto_test.go', '''package service

import (
    "errors"
    "strings"
    "testing"
    "github.com/LIghtJUNction/api.lmm.best/common"
)

func TestMerchantStoreExtoreKeySelection(t *testing.T) {
    key := "extore-test-only-persistent-key-20261009-123456789"
    for _, tc := range []struct{name, merchant, crypto, session string; ok bool}{
        {"merchant-only", key, "", "", true},
        {"crypto-only", "", key, "", true},
        {"session-compatibility", "", "", key, true},
        {"missing", "", "", "", false},
        {"weak-merchant-does-not-fall-back", "short", key, key, false},
        {"weak-crypto-does-not-fall-back", "", "short", key, false},
    } {
        t.Run(tc.name, func(t *testing.T) {
            t.Setenv("MERCHANT_STORE_ENCRYPTION_KEY", tc.merchant)
            t.Setenv("CRYPTO_SECRET", tc.crypto)
            t.Setenv("SESSION_SECRET", tc.session)
            ciphertext, err := EncryptMerchantStoreExtoreFlow("private-verifier")
            if !tc.ok {
                if !errors.Is(err, common.ErrPersistentKeyUnavailable) || ciphertext != "" { t.Fatal("must fail closed", err) }
                return
            }
            if err != nil || strings.Contains(ciphertext, "private-verifier") { t.Fatal("unsafe encryption", err) }
            plaintext, err := DecryptMerchantStoreExtoreFlow(ciphertext)
            if err != nil || plaintext != "private-verifier" { t.Fatal("round trip failed", err) }
        })
    }
}

func TestMerchantStoreExtoreOldFlowsSurviveAddingMerchantKey(t *testing.T) {
    t.Setenv("MERCHANT_STORE_ENCRYPTION_KEY", "")
    t.Setenv("CRYPTO_SECRET", "legacy-extore-test-only-20261009-123456789")
    old, err := EncryptMerchantStoreExtoreFlow("old-verifier")
    if err != nil { t.Fatal(err) }
    t.Setenv("MERCHANT_STORE_ENCRYPTION_KEY", "new-extore-test-only-20261009-987654321")
    got, err := DecryptMerchantStoreExtoreFlow(old)
    if err != nil || got != "old-verifier" { t.Fatal("legacy flow lost", err) }
    current, err := EncryptMerchantStoreExtoreFlow("new-verifier")
    if err != nil || !strings.HasPrefix(current, merchantExtoreEnvelope) { t.Fatal("missing key envelope", err) }
    t.Setenv("MERCHANT_STORE_ENCRYPTION_KEY", "")
    if _, err := DecryptMerchantStoreExtoreFlow(current); !errors.Is(err, common.ErrPersistentKeyUnavailable) { t.Fatal("must not guess a fallback key", err) }
}
''')
append('apps/api-go/internal/extore/client_test.go', '''
func TestCatalogAllowsEmptyVariantsButRejectsMissingAndOversizedVariants(t *testing.T) {
    for _, count := range []int{0, 100, 101} {
        c := fixture(t)
        var listing map[string]json.RawMessage
        if err := json.Unmarshal(c.Products[0], &listing); err != nil { t.Fatal(err) }
        variants := make([]map[string]any, count)
        for i := range variants { variants[i] = map[string]any{"price": nil, "attributes": map[string]any{}} }
        listing["variants"], _ = json.Marshal(variants)
        c.Products[0], _ = json.Marshal(listing)
        err := ValidateCatalog(c, c.Issuer, c.GrantID)
        if (err == nil) != (count <= 100) { t.Fatalf("count %d: %v", count, err) }
        delete(listing, "variants")
        c.Products[0], _ = json.Marshal(listing)
        if !errors.Is(ValidateCatalog(c, c.Issuer, c.GrantID), ErrProtocol) { t.Fatal("accepted missing variants") }
    }
}
''')
p = 'apps/web/src/features/store/extore-import-protocol.ts'
edit(p, '    id,\n    name: text(200),\n    description: text(10000).optional(),', "    id: z.string().regex(/^[a-z0-9][a-z0-9_-]{0,39}$/).optional(),\n    name: z.string().max(120).optional(),\n    description: z.string().max(10000).optional(),")
edit(p, 'currency: z.string().regex(/^[A-Z]{3,5}$/),', 'currency: z.string().regex(/^[A-Z]{3,5}$/).optional(),')
edit(p, 'enabled: z.boolean(),', 'enabled: z.boolean().optional(),')
edit(p, '        name: text(200),\n        description: text(20000),\n        public: z.boolean(),', '        name: text(120).optional(),\n        description: text(20000).optional(),\n        public: z.boolean().optional(),')
edit(p, 'variants: z.array(variantSchema).min(1).max(200)', 'variants: z.array(variantSchema).max(100)')
edit(p, '''      const variants = new Set(listing.variants.map((variant) => variant.id))
      if (variants.size !== listing.variants.length) throw new Error()''', '''      const ids = listing.variants.flatMap((variant) => variant.id ? [variant.id] : [])
      if (new Set(ids).size !== ids.length) throw new Error()''')
edit(p, '    !variant.enabled ||', '    !variant.id || variant.enabled !== true ||')
edit(p, '''  const variantName = extoreText(variant.name, language).trim()
  if (!name || !variantName) throw new Error(EXTORE_INVALID_CATALOG)
  const title = listing.variants.length > 1 ? `${name} · ${variantName}` : name''', '''  const variantName = extoreText(variant.name, language).trim() || variant.id
  // A missing title stays blank for the seller to supply in the existing editor.
  const title = name && listing.variants.length > 1 ? `${name} · ${variantName}` : name''')
edit(p, "visibility: listing.product.public ? 'public' : 'private'", "visibility: listing.product.public === true ? 'public' : 'private'")
edit(p, 'referenceCurrency: variant.currency,', "referenceCurrency: variant.currency ?? '',")
p = 'apps/web/src/features/store/extore-import.tsx'
edit(p, '(item) => item.id === variantId && item.enabled', '(item) => !!item.id && item.id === variantId && item.enabled === true')
edit(p, 'listing?.product.public !== false || accessSupported', 'listing?.product.public === true || accessSupported')
edit(p, '{extoreText(item.product.name, i18n.language)}', '{extoreText(item.product.name, i18n.language) || item.id}')
edit(p, '''                        {listing.variants.map((item) => (
                          <option
                            key={item.id}
                            value={item.id}
                            disabled={!item.enabled}
                          >
                            {extoreText(item.name, i18n.language)}
                            {!item.enabled ? ` · ${t('Disabled')}` : ''}
                          </option>
                        ))}''', '''                        {listing.variants.map((item, index) => (
                          <option
                            key={item.id ?? `missing-${index}`}
                            value={item.id ?? ''}
                            disabled={!item.id || item.enabled !== true}
                          >
                            {extoreText(item.name, i18n.language) || item.id || t('Not provided')}
                            {(!item.id || item.enabled !== true) ? ` · ${t('Disabled')}` : ''}
                          </option>
                        ))}''')
edit(p, 'listing.variants.some((item) => item.enabled)', 'listing.variants.some((item) => item.id && item.enabled === true)')
edit(p, '`${variant.price} ${variant.currency}`', "`${variant.price} ${variant.currency ?? t('Not provided')}`")
edit(p, 'listing.product.public === false', 'listing.product.public !== true')
append('apps/web/src/features/store/extore-import-protocol.test.ts', '''
test('optional upstream fields do not invalidate the entire catalog or invent price and visibility', () => {
  const input = fixture()
  Reflect.deleteProperty(input.products[0].product, 'description')
  Reflect.deleteProperty(input.products[0].product, 'public')
  Reflect.deleteProperty(input.products[0].product, 'name')
  Reflect.deleteProperty(input.products[0].variants[0], 'name')
  Reflect.deleteProperty(input.products[0].variants[0], 'currency')
  const catalog = parseExtoreCatalog(input)
  const product = catalog.products[0]
  const draft = extoreProductDraft(catalog, product, product.variants[0], 'en')
  assert.equal(draft.fields.title, '')
  assert.equal(draft.fields.description, '')
  assert.equal(draft.fields.visibility, 'private')
  assert.equal(draft.referenceCurrency, '')
  assert.equal(draft.fields.price_quota, undefined)
})

test('missing variant id or enabled flag is previewable but cannot grant import permission', () => {
  for (const field of ['id', 'enabled']) {
    const input = fixture()
    Reflect.deleteProperty(input.products[0].variants[0], field)
    const catalog = parseExtoreCatalog(input)
    assert.throws(() => extoreProductDraft(catalog, catalog.products[0], catalog.products[0].variants[0], 'en'))
  }
})

test('upstream variants boundary permits zero and 100, not 101', () => {
  for (const count of [0, 100, 101]) {
    const input = fixture()
    input.products[0].variants = Array.from({length: count}, (_, i) => ({...input.products[0].variants[0], id: `v${i}`}))
    if (count <= 100) assert.equal(parseExtoreCatalog(input).products[0].variants.length, count)
    else assert.throws(() => parseExtoreCatalog(input))
  }
})
''')
policy = "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; font-src 'self' data:; connect-src 'self'; worker-src 'self' blob:; object-src 'none'; base-uri 'none'; form-action 'self'; frame-src 'none'"
add('apps/web/public/extore-callback.js', '''/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
;(() => {
  if (window.location.pathname !== '/store/manage') return
  const query = new URLSearchParams(window.location.search)
  if (!['code', 'state', 'iss', 'error', 'error_description'].some((key) => query.has(key))) return
  window.__lmmExtoreCallbackPage = true
  Object.defineProperty(window, '__lmmExtoreCallback', {
    value: window.location.href,
    configurable: true,
  })
  const referrer = document.createElement('meta')
  referrer.name = 'referrer'
  referrer.content = 'no-referrer'
  document.head.appendChild(referrer)
  const csp = document.createElement('meta')
  csp.httpEquiv = 'Content-Security-Policy'
  csp.content = "''' + policy + '''"
  document.head.appendChild(csp)
  window.history.replaceState(window.history.state, '', '/store/manage')
})()
''')
p = 'apps/web/index.html'
edit(p, '    <meta charset="UTF-8" />', '    <meta charset="UTF-8" />\n    <script src="/extore-callback.js"></script>')
edit(p, '''    <script
      async
      src="https://cdn.agentlane.com/v1/snippet.js"
      data-domain="dom-plf25ce17li9"
    ></script>''', '''    <script>
      // Also fail closed if the local callback bootstrap could not load.
      if (!window.__lmmExtoreCallbackPage &&
          !(location.pathname === '/store/manage' && location.search)) {
        const tracker = document.createElement('script')
        tracker.async = true
        tracker.src = 'https://cdn.agentlane.com/v1/snippet.js'
        tracker.dataset.domain = 'dom-plf25ce17li9'
        document.head.appendChild(tracker)
      }
    </script>''')
add('apps/web/src/features/store/extore-callback.ts', '''/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
declare global {
  interface Window {
    __lmmExtoreCallback?: string
    __lmmExtoreCallbackPage?: boolean
  }
}

export function extoreCallbackURL(): string | null {
  const raw = window.__lmmExtoreCallback ?? window.location.href
  const url = new URL(raw)
  return url.pathname === '/store/manage' &&
    ['code', 'state', 'iss', 'error', 'error_description'].some((key) => url.searchParams.has(key))
    ? raw : null
}

export function clearExtoreCallbackURL(): void {
  delete window.__lmmExtoreCallback
  window.history.replaceState(window.history.state, '', '/store/manage')
}
''')
p = 'apps/web/src/features/store/seller-page.tsx'
edit(p, "import { StoreExtoreImport } from './extore-import'", "import { StoreExtoreImport } from './extore-import'\nimport { extoreCallbackURL, clearExtoreCallbackURL } from './extore-callback'")
edit(p, '''  const [extoreCallback, setExtoreCallback] = useState<string | null>(() => {
    const query = new URLSearchParams(window.location.search)
    return query.has('state') &&
      query.has('iss') &&
      (query.has('code') || query.has('error'))
      ? window.location.href
      : null
  })''', '''  const [extoreCallback, setExtoreCallback] = useState(extoreCallbackURL)''')
edit(p, "      window.history.replaceState(window.history.state, '', '/store/manage')", '      clearExtoreCallbackURL()')
p = 'apps/api-go/router/main.go'
edit(p, '\trequestPath := path.Clean("/" + request.URL.Path)', '''\trequestPath := path.Clean("/" + request.URL.Path)
    if requestPath == "/store/manage" {
        writer.Header().Set("Cache-Control", "no-store")
        writer.Header().Set("Referrer-Policy", "no-referrer")
        if request.URL.RawQuery != "" {
            writer.Header().Set("Content-Security-Policy", "''' + policy + '''")
        }
    }''')
p = 'apps/api-go/middleware/logger.go'
edit(p, 'if path == "/api/store" || strings.HasPrefix(path, "/api/store/") {', 'if path == "/store/manage" || path == "/api/store" || strings.HasPrefix(path, "/api/store/") {')
p = 'packaging/common/lmm-api/edge-policy/nginx/http-map.conf'
edit(p, 'map $request_uri $lmm_store_request_loggable {\n    default 1;', 'map $request_uri $lmm_store_request_loggable {\n    default 1;\n    ~^/store/manage(?:/|\\?|$) 0;')
edit(p, 'map $http_referer $lmm_store_referer_loggable {\n    default 1;', 'map $http_referer $lmm_store_referer_loggable {\n    default 1;\n    ~*^https?://[^/]+/store/manage(?:/|\\?|$) 0;')
location = '''location = /store/manage {
    access_log off;
    try_files /index.html =404;
    add_header Cache-Control "no-store" always;
    add_header Referrer-Policy "no-referrer" always;
    set $lmm_extore_callback_csp "";
    if ($args != "") { set $lmm_extore_callback_csp "''' + policy + '''"; }
    add_header Content-Security-Policy $lmm_extore_callback_csp always;
}
'''
p = 'packaging/common/lmm-api/edge-policy/nginx/lmm-api-locations.conf'
edit(p, '# These are client-side routes, including the browser half of OAuth.', location + '\n# These are client-side routes, including the browser half of OAuth.')
p = 'scripts/render-frontend-proxy-overlay.py'
edit(p, "    for selector in ('= /index.html'", "    overlay += " + repr(location.replace('    access_log off;\n', '    error_page 418 = @lmm_api_backend;\n    if ($request_method !~ "^(GET|HEAD)$") { return 418; }\n    access_log off;\n')) + "\n    for selector in ('= /index.html'")
add('apps/api-go/router/extore_callback_test.go', '''package router

import (
    "net/http/httptest"
    "os"
    "path/filepath"
    "strings"
    "testing"
)

func TestExtoreCallbackFrontendHeaders(t *testing.T) {
    root := t.TempDir()
    if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("<!doctype html>callback"), 0600); err != nil { t.Fatal(err) }
    handler, err := newFrontendHandler(root)
    if err != nil { t.Fatal(err) }
    for _, path := range []string{"/store/manage?code=fixture&state=fixture", "/store/manage?error=access_denied", "/store/manage"} {
        w := httptest.NewRecorder()
        handler.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
        if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Referrer-Policy") != "no-referrer" { t.Fatal("unprotected callback", w.Code, w.Header()) }
        if strings.Contains(path, "?") && !strings.Contains(w.Header().Get("Content-Security-Policy"), "connect-src 'self'") { t.Fatal("missing callback CSP") }
    }
}
''')
add('apps/api-go/middleware/extore_callback_test.go', '''package middleware

import (
    "net/http/httptest"
    "testing"
    "github.com/gin-gonic/gin"
)

func TestExtoreCallbackLoggerRemovesSecrets(t *testing.T) {
    raw := "/store/manage?code=fixture-code&state=fixture-state&iss=https%3A%2F%2Fextore.example"
    r := httptest.NewRequest("GET", raw, nil)
    got := loggedRequestPath(gin.LogFormatterParams{Request:r, Path:raw})
    if got != "/store/manage" { t.Fatal("callback query was logged") }
    if r.URL.RequestURI() != raw { t.Fatal("logging modified the real callback") }
}
''')
add('apps/web/scripts/extore-callback.test.mjs', '''/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'
import vm from 'node:vm'

const bootstrap = readFileSync(new URL('../public/extore-callback.js', import.meta.url), 'utf8')
const html = readFileSync(new URL('../index.html', import.meta.url), 'utf8')
const tracker = html.match(/<script>\\s*\\/\\/ Also fail closed[\\s\\S]*?<\\/script>/)[0].replace(/<\\/?script>/g, '')
function page(path, runBootstrap = true) {
  const location = new URL('https://shop.example' + path)
  const elements = []
  const document = {createElement: (tag) => ({tag, dataset:{}}), head:{appendChild: (el) => elements.push(el)}}
  const history = {state: null, replaceState: (_s, _t, value) => {location.href = new URL(value, location).href}}
  const window = {location, history}
  const context = vm.createContext({window, location, document, URLSearchParams})
  if (runBootstrap) vm.runInContext(bootstrap, context)
  vm.runInContext(tracker, context)
  return {window, elements}
}
test('captures callbacks before trackers, strips history, and blocks external connections', () => {
  for (const query of ['code=fixture&state=fixture&iss=fixture', 'error=access_denied&state=fixture', 'code=one&code=two', 'state=only']) {
    const {window, elements} = page('/store/manage?' + query)
    assert.equal(window.__lmmExtoreCallback, 'https://shop.example/store/manage?' + query)
    assert.equal(window.location.href, 'https://shop.example/store/manage')
    assert.equal(elements.some((el) => el.tag === 'script'), false)
    assert.equal(elements.find((el) => el.name === 'referrer')?.content, 'no-referrer')
    assert.match(elements.find((el) => el.httpEquiv === 'Content-Security-Policy')?.content, /connect-src 'self'/)
  }
})
test('ordinary pages keep analytics; a missing bootstrap never enables callback tracking', () => {
  assert.equal(page('/').elements.filter((el) => el.tag === 'script').length, 1)
  assert.equal(page('/store/manage?code=fixture', false).elements.filter((el) => el.tag === 'script').length, 0)
  assert.ok(html.indexOf('/extore-callback.js') < html.indexOf('rel="icon"'))
  assert.doesNotMatch(bootstrap, /localStorage|sessionStorage|fetch\\(/)
})
''')
append('docs/development/store-extore-import.md', '''
## 2026-10-09 协议适配修正

- 授权会话优先使用已配置的 `MERCHANT_STORE_ENCRYPTION_KEY`，没有该项时保留 `CRYPTO_SECRET` / `SESSION_SECRET` 的原有顺序。新商店密钥会话使用明确的密文标记；旧的十分钟会话仍按旧配置读取，不尝试其他密钥来掩盖解密失败。非空弱密钥仍拒绝使用。不要为此覆盖、打印或重新生成生产中已有的密钥。
- 未配置安全存储时，在联系 Extore 前返回 `STORE_EXTORE_SECURE_STORAGE`，提示管理员检查配置，不再引导用户反复重试。没有任何合格密钥时仍拒绝明文保存。
- 目录支持上游可省略的描述、名称、币种与标志。未知标题保留空白供商家补充；未知币种不换汇，未知可见性按私有处理。缺少规格 ID 或启用标志的规格只能预览，不能导入。空规格目录可以预览，每商品上限对齐为 100，服务端目录大小上限对齐为 16 MiB。
- 回调仍是原登记的 `/store/manage`，无需重新登记客户端。页面最早加载本地回调脚本，在加载应用前把参数移到本页内存并清理地址栏。回调文档不加载外部跟踪脚本，并使用只允许本站连接和图片的内容策略；该次页面中的外部图片预览会受限，正常重新打开卖家中心后恢复。
- Go 静态前端、软件包 Nginx 与代理前端覆盖配置均提供 `no-store`、`no-referrer` 和回调内容策略。Go 日志去掉回调查询参数，Nginx 排除回调及其来源日志。其他反向代理也必须过滤回调 URL 与 Referer；只升级前端不能替代服务器日志保护。

部署需同时更新 Go、前端及所使用的边缘配置。本次不修改生产环境变量，不发行卡密，不增加自动上架、持续连接或补货权限。
''')
p='docs/development/store-extore-import.md'
edit(p, '需要稳定配置 `CRYPTO_SECRET` 或 `SESSION_SECRET`。', '需要稳定配置 `MERCHANT_STORE_ENCRYPTION_KEY`，或保留已有的 `CRYPTO_SECRET` / `SESSION_SECRET` 配置。')
edit(p, '商品目录最多 4 MiB；目录最多 100 个商品、每商品 200 个规格。', '商品目录最多 16 MiB；目录最多 100 个商品、每商品 100 个规格。')
Path('/tmp/extore-changed.json').write_text(json.dumps(changed))
print('\n'.join(changed))
