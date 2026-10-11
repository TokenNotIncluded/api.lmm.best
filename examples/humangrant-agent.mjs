#!/usr/bin/env node
// One independent client process: create a key, forward the human-only link,
// read real Markdown, claim once, and read exactly the approved profile.
// Keep stdout private: the human authorization link is not a public share URL.
import { newActor, signedFetch, parseState, HGError } from '../apps/api-go/humangrant/web/client.mjs'

const [origin, account] = process.argv.slice(2)
if (!origin || !/^[1-9][0-9]{0,17}$/.test(account ?? '') || new URL(origin).origin !== origin || new URL(origin).protocol !== 'https:') {
  console.error('Usage: node examples/humangrant-agent.mjs https://YOUR-LMM-ORIGIN ACCOUNT_ID')
  process.exit(2)
}
const actor = await newActor()
const request = await signedFetch(actor, origin, 'POST', '/v1/requests', {
  client_id: 'lmm-agent', target_service: 'lmm-api', purpose: 'Read the approved account profile',
  expected_human_id: account, requested_permissions: [{ action: 'account.profile.read', resource: account }],
})
if (new URL(request.human_authorization_url).origin !== origin) throw new Error('Unexpected human authorization origin')
console.log(request.forwarding_prompt)
let interval = Math.max(5, request.poll_after_seconds), failures = 0, done = false
while (Date.now() / 1000 < request.expires_at) {
  await new Promise(resolve => setTimeout(resolve, interval * 1000))
  let state
  try {
    const markdown = await signedFetch(actor, origin, 'GET', '/v1/requests/' + request.request_id + '/status.md')
    state = parseState(markdown); failures = 0
    if (state.request_id !== request.request_id || state.target_service !== 'lmm-api' || state.client_id !== 'lmm-agent') throw new Error('State binding mismatch')
    console.log('Phase:', state.phase)
  } catch (error) {
    if (error instanceof HGError && error.status === 429) { interval = Math.min(60, Math.max(interval, error.retry)); continue }
    if (error instanceof HGError && error.status < 500) throw error
    if (++failures >= 6) throw error
    interval = Math.min(60, interval * 2); continue
  }
  interval = Math.max(interval, state.poll_after_seconds)
  if (state.phase === 'approved') {
    // Never retry an uncertain claim. The human can revoke a lost grant at
    // /human/grants; a new authorization requires a new explicit request.
    const session = await signedFetch(actor, origin, 'POST', '/v1/requests/' + request.request_id + '/claim', {})
    const profile = await signedFetch(actor, origin, 'GET', '/v1/accounts/' + account + '/profile', undefined, session.access_token)
    console.log(JSON.stringify(profile, null, 2))
    done = true; break
  }
  if (['succeeded', 'rejected', 'cancelled', 'expired'].includes(state.phase)) throw new Error('Request ended: ' + state.phase)
}
if (!done) throw new Error('Request expired')
