from pathlib import Path
import json
changed=[]
def edit(path,old,new):
 p=Path(path); text=p.read_text()
 if text.count(old)!=1: raise RuntimeError(f'{path}: expected one source match')
 p.write_text(text.replace(old,new)); changed.append(path)
def add(path,text):
 p=Path(path)
 if p.exists(): raise RuntimeError(f'{path}: already exists')
 p.parent.mkdir(parents=True,exist_ok=True); p.write_text(text); changed.append(path)

message='Extore import needs a persistent server encryption key. Ask an administrator to configure it.'
privacy='This draft stays private. Review its visibility before publishing.'
translations={
 'en':[message,privacy],
 'zh':['Extore 导入需要服务器配置持久加密密钥。请联系管理员检查配置。','此草稿保持私有。发布前请确认可见性。'],
 'zh-TW':['Extore 匯入需要伺服器設定持久加密金鑰。請聯絡管理員檢查設定。','此草稿保持私人狀態。發布前請確認可見性。'],
 'fr':['L’import Extore nécessite une clé de chiffrement persistante sur le serveur. Demandez à un administrateur de la configurer.','Ce brouillon reste privé. Vérifiez sa visibilité avant publication.'],
 'ja':['Extore のインポートには、サーバーに永続的な暗号化キーを設定する必要があります。管理者に設定の確認を依頼してください。','この下書きは非公開のままです。公開前に表示設定を確認してください。'],
 'ru':['Для импорта из Extore нужен постоянный ключ шифрования на сервере. Попросите администратора настроить его.','Этот черновик остаётся закрытым. Проверьте настройки видимости перед публикацией.'],
 'vi':['Nhập từ Extore cần khóa mã hóa cố định trên máy chủ. Hãy yêu cầu quản trị viên kiểm tra cấu hình.','Bản nháp này vẫn ở chế độ riêng tư. Kiểm tra chế độ hiển thị trước khi xuất bản.']}
for language,values in translations.items():
 path=f'apps/web/src/i18n/locales/{language}.json'
 addition=''.join('    '+json.dumps(k,ensure_ascii=False)+': '+json.dumps(v,ensure_ascii=False)+',\n' for k,v in zip([message,privacy],values))
 edit(path,'  "translation": {\n','  "translation": {\n'+addition)
for language in ['en','zh']:
 addition=''.join('    '+json.dumps(k,ensure_ascii=False)+': '+json.dumps(v,ensure_ascii=False)+',\n' for k,v in zip([message,privacy],translations[language]))
 edit('apps/web/scripts/merchant-store-copy.mjs',f'  {language}: {{\n',f'  {language}: {{\n'+addition)
edit('apps/web/src/features/store/extore-import-copy.ts',"'This is a private Extore product. It stays private in the draft.'",repr(privacy))
edit('apps/web/package.json','"test": "bun scripts/test-serial.mjs"','"test": "node --test scripts/extore-callback.test.mjs && bun scripts/test-serial.mjs"')
edit('apps/api-go/controller/merchant_store_extore.go','func storeExtoreError(c *gin.Context, err error) {\n','func storeExtoreError(c *gin.Context, err error) {\n\tc.Header("Cache-Control", "no-store")\n')
add('apps/api-go/controller/merchant_store_extore_crypto_test.go','''package controller

import (
    "encoding/json"
    "net/http/httptest"
    "net/url"
    "testing"
    "time"

    "github.com/LIghtJUNction/api.lmm.best/common"
    "github.com/LIghtJUNction/api.lmm.best/internal/extore"
    "github.com/LIghtJUNction/api.lmm.best/model"
    "github.com/LIghtJUNction/api.lmm.best/service"
    "github.com/gin-gonic/gin"
    "github.com/stretchr/testify/require"
)

func TestMerchantStoreExtoreMerchantKeyCallback(t *testing.T) {
    t.Setenv("MERCHANT_STORE_ENCRYPTION_KEY", "merchant-extore-callback-fixture-20261009-123456789")
    t.Setenv("CRYPTO_SECRET", "")
    t.Setenv("SESSION_SECRET", "")
    db := setupManageUserTestDB(t)
    require.NoError(t, db.AutoMigrate(&model.AuthFlow{}))
    flow, err := extore.NewFlow("https://extore.example", "client", "https://shop.example/store/manage")
    require.NoError(t, err)
    raw, err := json.Marshal(flow)
    require.NoError(t, err)
    encrypted, err := service.EncryptMerchantStoreExtoreFlow(string(raw))
    require.NoError(t, err)
    require.NotContains(t, encrypted, flow.Verifier)
    state, _, err := model.CreateAuthFlow(model.AuthFlowCreate{Purpose: storeExtorePurpose, UserId: 7, SessionId: "session-a", Payload: encrypted, ExpiresAt: time.Now().Add(time.Minute)})
    require.NoError(t, err)
    callback := flow.RedirectURI + "?error=access_denied&state=" + state + "&iss=" + url.QueryEscape(flow.Origin)
    response := extoreCallbackRequest(t, 7, "session-a", callback)
    require.Equal(t, 403, response.Code, response.Body.String())
    require.Equal(t, 409, extoreCallbackRequest(t, 7, "session-a", callback).Code)
}

func TestMerchantStoreExtoreStorageErrorIsActionable(t *testing.T) {
    response := httptest.NewRecorder()
    c, _ := gin.CreateTestContext(response)
    storeExtoreError(c, common.ErrPersistentKeyUnavailable)
    require.Equal(t, 503, response.Code)
    require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
    var body struct {Code string `json:"code"`; Message string `json:"message"`}
    require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
    require.Equal(t, "STORE_EXTORE_SECURE_STORAGE", body.Code)
    require.Contains(t, body.Message, "administrator")
    require.NotContains(t, body.Message, "later")
}
''')
add('apps/web/src/features/store/extore-callback.test.ts','''/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { extoreCallbackURL, clearExtoreCallbackURL } from './extore-callback'

test('the early callback survives repeated initialization and retains duplicate parameters for server validation', () => {
  const previous = Object.getOwnPropertyDescriptor(globalThis, 'window')
  const raw = 'https://shop.example/store/manage?code=one&code=two&state=fixture&iss=fixture'
  const location = new URL('https://shop.example/store/manage')
  const bridge = {
    __lmmExtoreCallback: raw,
    __lmmExtoreCallbackPage: true,
    location,
    history: {state: null, replaceState: (_state: unknown, _title: string, value: string) => {location.href = new URL(value, location).href}},
  }
  Object.defineProperty(globalThis, 'window', {configurable: true, value: bridge})
  try {
    assert.equal(extoreCallbackURL(), raw)
    assert.equal(extoreCallbackURL(), raw)
    clearExtoreCallbackURL()
    assert.equal(extoreCallbackURL(), null)
    assert.equal(bridge.__lmmExtoreCallback, undefined)
    assert.equal(bridge.__lmmExtoreCallbackPage, true)
    assert.equal(location.href, 'https://shop.example/store/manage')
  } finally {
    if (previous) Object.defineProperty(globalThis, 'window', previous)
    else Reflect.deleteProperty(globalThis, 'window')
  }
})
''')
add('scripts/nginx-extore-callback.test.mjs','''import assert from 'node:assert/strict'
import { spawn, execFileSync } from 'node:child_process'
import { mkdtempSync, readFileSync, writeFileSync, chmodSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { createServer } from 'node:net'
import { once } from 'node:events'
import { setTimeout as delay } from 'node:timers/promises'
import { test } from 'node:test'

// Run with nginx installed. All requests and credentials are local fixtures.
test('Extore callback headers and request/referrer log exclusions work in nginx', async () => {
  const root = mkdtempSync(join(tmpdir(), 'extore-nginx-'))
  chmodSync(root, 0o755)
  const locations = readFileSync(new URL('../packaging/common/lmm-api/edge-policy/nginx/lmm-api-locations.conf', import.meta.url), 'utf8')
  const location = locations.match(/location = \\/store\\/manage \\{[\\s\\S]*?\\n\\}/)?.[0]
  assert.ok(location)
  const maps = readFileSync(new URL('../packaging/common/lmm-api/edge-policy/nginx/http-map.conf', import.meta.url), 'utf8').split('# Use the original URI:')[1]
  assert.ok(maps)
  // Drop only the remainder of the marker's comment line, not any map directive.
  const logMaps = maps.slice(maps.indexOf('\\n'))
  const portServer = createServer()
  portServer.listen(0, '127.0.0.1')
  await once(portServer, 'listening')
  const address = portServer.address()
  assert.ok(address && typeof address !== 'string')
  const port = address.port
  await new Promise((resolve) => portServer.close(resolve))
  writeFileSync(join(root, 'index.html'), '<!doctype html>Extore callback fixture')
  const log = join(root, 'access.log')
  const config = `pid ${root}/nginx.pid; error_log ${root}/error.log; events {} http { ${logMaps} server { listen 127.0.0.1:${port}; root ${root}; access_log ${log} combined if=$lmm_access_loggable; ${location} location = /asset.js { return 200 'fixture'; } } }`
  writeFileSync(join(root, 'nginx.conf'), config)
  let server
  try {
    execFileSync('nginx', ['-t', '-p', root, '-c', join(root, 'nginx.conf')], {stdio:'pipe'})
    server = spawn('nginx', ['-p', root, '-c', join(root, 'nginx.conf'), '-g', 'daemon off; master_process off;'], {stdio:'ignore'})
    const origin = `http://127.0.0.1:${port}`
    let ready = false
    for (let n = 0; n < 50; n++) {
      try { await fetch(origin + '/asset.js'); ready = true; break } catch { await delay(50) }
    }
    assert.ok(ready, 'nginx did not start')
    const callback = origin + '/store/manage?code=fixture-code&state=fixture-state&iss=fixture'
    const response = await fetch(callback)
    assert.equal(response.status, 200)
    assert.equal(response.headers.get('cache-control'), 'no-store')
    assert.equal(response.headers.get('referrer-policy'), 'no-referrer')
    assert.match(response.headers.get('content-security-policy'), /connect-src 'self'/)
    assert.match(await response.text(), /Extore callback fixture/)
    await fetch(origin + '/asset.js', {headers:{referer:callback}})
    const ordinary = await fetch(origin + '/store/manage')
    assert.equal(ordinary.headers.get('content-security-policy'), null)
    const stopped = once(server, 'exit')
    server.kill('SIGTERM')
    await stopped
    server = undefined
    const output = readFileSync(log, 'utf8')
    assert.match(output, /GET \\/asset.js/)
    assert.doesNotMatch(output, /fixture-code|fixture-state|store\\/manage/)
  } finally {
    if (server && server.exitCode === null) { const stopped = once(server, 'exit'); server.kill('SIGTERM'); await stopped }
    rmSync(root, {recursive:true, force:true})
  }
})
''')
edit('.github/workflows/ci.yml','node --test scripts/nginx-service-errors.test.mjs','node --test scripts/nginx-service-errors.test.mjs scripts/nginx-extore-callback.test.mjs')
Path('/tmp/extore-changed.json').write_text(json.dumps(sorted(set(changed))))
print('\n'.join(sorted(set(changed))))
