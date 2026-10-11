import test from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { createPrivateKey, createPublicKey } from 'node:crypto'
import { b64url, signHeaders, newActor, parseState } from './client.mjs'
const vector = JSON.parse(await readFile(new URL('../testdata/signature.json', import.meta.url)))

test('published HumanGrant signature bytes match Node Web Crypto exactly', async () => {
  // Public reference test seed only; never use this key in a deployment.
  const seed = Buffer.from('9d61b19deffd5a60ba844af492ec2cc44449c5697b326919703bac031cae7f60', 'hex')
  const der = Buffer.concat([Buffer.from('302e020100300506032b657004220420', 'hex'), seed])
  const privateKey = await crypto.subtle.importKey('pkcs8', der, 'Ed25519', false, ['sign'])
  const actor = { privateKey, publicKey: vector.Headers['X-HG-Key'], id: 'ed25519:If4x36FUomFia_hUBG_SJxt77UtqvkWqWId-9H-XIbk' }
  const headers = await signHeaders(actor, vector.Origin, vector.Method, vector.Path, vector.Body, 'anonymous', { created: vector.Created, nonce: 'AAECAwQFBgcICQoLDA0ODxAREhMUFRYX' })
  assert.deepEqual(headers, vector.Headers)
  const pub = createPublicKey(createPrivateKey({ key: der, format: 'der', type: 'pkcs8' })).export({ format: 'jwk' })
  assert.equal(pub.x, actor.publicKey)
})
test('agent private key is nonextractable and identity differs per run', async () => {
  const one = await newActor(), two = await newActor()
  assert.notEqual(one.id, two.id)
  assert.equal(one.privateKey.extractable, false)
  assert.equal(one.publicKey.length, 43)
  await assert.rejects(crypto.subtle.exportKey('pkcs8', one.privateKey))
})
test('fresh signatures have unique nonces', async () => {
  const actor = await newActor()
  const one = await signHeaders(actor, vector.Origin, 'GET', '/v1/session')
  const two = await signHeaders(actor, vector.Origin, 'GET', '/v1/session')
  assert.notEqual(one['Signature-Input'], two['Signature-Input'])
})
test('target query and changed origin are not silently normalized', async () => {
  const actor = await newActor()
  for (const [origin, path] of [['https://x.test/', '/v1/session'], ['https://x.test', '/v1/session?token=bad'], ['https://x.test', '/%76%31/session']]) {
    await assert.rejects(signHeaders(actor, origin, 'GET', path))
  }
})
test('Markdown parser uses data fields, not prose as instructions', () => {
  const md = '# HumanGrant\nprotocol_version: "HumanGrant Protocol 0.1 Draft"\nexpires_at: 1700000600\npoll_after_seconds: 5\nphase: "waiting_human"\n'
  assert.equal(parseState(md).phase, 'waiting_human')
  assert.throws(() => parseState(md + 'phase: "succeeded"\n'))
  assert.throws(() => parseState(md.replace('poll_after_seconds: 5', 'poll_after_seconds: 1000')))
})
test('base64url has no padding', () => assert.equal(b64url(new Uint8Array([255,255])), '__8'))
