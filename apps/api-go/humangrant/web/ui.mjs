import { newActor, signedFetch, parseState, HGError } from '/humangrant/client.js'
const { mode, data } = JSON.parse(document.getElementById('hg-data').textContent)
const app = document.getElementById('app'), notice = document.getElementById('notice')
const say = text => { notice.textContent = text }
const el = (tag, text, parent = app) => { const node = document.createElement(tag); if (text !== undefined) node.textContent = text; parent.append(node); return node }
const button = (text, action, parent = app) => { const b = el('button', text, parent); b.type = 'button'; b.addEventListener('click', async () => { b.disabled = true; try { await action() } catch (error) { say(error.message) } finally { b.disabled = false } }); return b }
const field = (text, type = 'text', value = '', parent = app) => { const label = el('label', text, parent), input = el('input', undefined, label); input.type = type; input.value = value; return input }
const link = (text, href, parent = app) => { const a = el('a', text, parent); a.href = href; a.target = '_blank'; a.rel = 'noopener noreferrer'; return a }
const display = (value, parent = app) => el('pre', typeof value === 'string' ? value : JSON.stringify(value, null, 2), parent)
const browserBase = '/api/user/auth/humangrant'
async function humanAPI(path, method = 'GET', value, csrf) {
  const headers = { 'Content-Type': 'application/json' }; if (csrf) headers['X-HG-CSRF'] = csrf
  const result = await fetch(path, { method, headers, body: value === undefined ? undefined : JSON.stringify(value), credentials: 'same-origin', cache: 'no-store', redirect: 'error', signal: AbortSignal.timeout(15000) })
  const json = await result.json(); if (!result.ok) throw new Error(json.error ?? 'Request failed'); return json
}
function signIn() { link('Sign in to your existing LMM account, then return here.', '/sign-in') }
async function human() {
  el('p', 'This AI acts as itself. Review its identity, account and exact access before approval. Your login alone does not approve this request.')
  display({ actor: data.actor, public_key: data.public_key, service: data.service, client: data.client, expected_account: data.expected_human_id, request_expires_at: data.expires_at })
  el('h2', 'Purpose supplied by the requester — untrusted text'); display(data.purpose)
  el('h2', 'Requested access'); display(data.permissions)
  el('p', 'Request expires: ' + new Date(data.expires_at * 1000).toLocaleString())
  signIn()
  let csrf = data.csrf
  const verified = el('section')
  button('Verify my signed-in account', async () => {
    const base = browserBase + '/human/' + data.link
    await humanAPI(base + '/begin', 'POST', {}, csrf)
    const result = await humanAPI(base + '/verify', 'POST', {}, csrf)
    csrf = result.csrf; verified.replaceChildren()
    el('h2', 'Account granting access', verified); display(result.account, verified)
    const choices = data.permissions.map(pair => { const input = field(pair.action + ' · account ' + pair.resource, 'checkbox', '', verified); input.checked = false; return { input, pair } })
    const utcValue = seconds => new Date(seconds * 1000).toISOString().slice(0, 19)
    const expiry = field('Access ends at (UTC)', 'datetime-local', utcValue(data.max_grant_expires_at), verified)
    expiry.step = '1'; expiry.max = utcValue(data.max_grant_expires_at); expiry.min = utcValue(Math.floor(Date.now() / 1000) + 1)
    button('Approve selected access', async () => {
      const permissions = choices.filter(c => c.input.checked).map(c => c.pair)
      if (!permissions.length) throw new Error('Select the access to approve.')
      const expires = Date.parse(expiry.value + 'Z') / 1000; if (!Number.isSafeInteger(expires)) throw new Error('Invalid expiry')
      await humanAPI(base + '/approve', 'POST', { permissions, expires_at: expires }, csrf)
      verified.replaceChildren(); say('Approved. The original AI must claim its own credential. This page cannot claim it.')
    }, verified)
    button('Reject request', async () => { await humanAPI(base + '/reject', 'POST', {}, csrf); verified.replaceChildren(); say('Rejected.') }, verified)
    say('Account verified. No access has been granted yet.')
  })
}
async function grants() {
  signIn()
  const list = el('section')
  async function load() {
    const result = await humanAPI(browserBase + '/grants'); list.replaceChildren()
    if (!result.grants.length) el('p', 'No grants.', list)
    if (result.truncated) el('p', 'Only the first 200 grants are shown. Use the operator procedure for a complete record.', list)
    for (const grant of result.grants) {
      const row = el('section', undefined, list); display(grant, row)
      if (grant.status !== 'revoked') button('Revoke', async () => { await humanAPI(browserBase + '/grants/' + grant.grant_id + '/revoke', 'POST', {}, result.csrf); await load() }, row)
    }
  }
  button('Load my grants', load)
}
async function invite() {
  el('p', 'Opening this page does not approve or reject access. These actions can only refuse invitations; they cannot grant access.')
  for (const action of ['reject', 'block']) button(action === 'block' ? 'Refuse and block future invitations' : 'Refuse this invitation', async () => { await humanAPI('/invite/' + data.token + '/' + action, 'POST', {}, data.csrf); say('Your choice was recorded.') })
}
async function agent() {
  el('p', 'Create an independent AI identity in this page. The key stays in memory. Closing or reloading the page loses it; a lost issued credential cannot be recovered. This does not sign the AI into the human dashboard.')
  const account = field('Account ID to request access to', 'text')
  account.inputMode = 'numeric'; account.pattern = '[1-9][0-9]*'
  const purpose = field('Purpose', 'text', 'Read the approved account profile')
  let actor, request, token, stopped = true, interval = 5, failures = 0
  const stateBox = el('section'), controls = el('section')
  const show = state => { stateBox.replaceChildren(); display(state, stateBox) }
  async function poll() {
    if (stopped) return
    if (Date.now() / 1000 >= request.expires_at) { stopped = true; say('Request expired.'); return }
    try {
      const markdown = await signedFetch(actor, data.origin, 'GET', '/v1/requests/' + request.request_id + '/status.md')
      const state = parseState(markdown)
      if (state.request_id !== request.request_id || state.client_id !== data.client_id || state.target_service !== data.target_service) { stopped = true; throw new Error('State binding mismatch') }
      show(markdown); failures = 0; interval = Math.max(interval, state.poll_after_seconds)
      if (state.phase === 'approved') {
        stopped = true
        // Claim exactly once. A timeout is uncertain, not permission to retry.
        try { token = await signedFetch(actor, data.origin, 'POST', '/v1/requests/' + request.request_id + '/claim', {}) }
        catch { say('Claim outcome is uncertain or failed. Do not retry. Check the request state and revoke the grant before requesting new access.'); return }
        say('AI session created. No human cookie was issued.'); display({ grant_id: token.grant_id, expires_at: token.expires_at }, controls)
        button('Read approved account profile', async () => { display(await signedFetch(actor, data.origin, 'GET', '/v1/accounts/' + account.value + '/profile', undefined, token.access_token), controls) }, controls)
        return
      }
      if (['succeeded', 'rejected', 'cancelled', 'expired'].includes(state.phase)) { stopped = true; say(state.status_text); return }
    } catch (error) {
      if (error instanceof HGError && error.status === 429) interval = Math.max(interval, error.retry, 5)
      else if (error instanceof HGError && error.status < 500) { stopped = true; say(error.message); return }
      else if (++failures >= 6) { stopped = true; say('Polling stopped after six failures. No new request was created.'); return }
      else interval = Math.min(60, Math.max(interval, 5 * 2 ** failures))
    }
    if (!stopped) setTimeout(poll, interval * 1000)
  }
  const start = button('Continue with human beings', async () => {
    if (request) throw new Error('This page has already created a request. Do not create another automatically.')
    if (!/^[1-9][0-9]{0,17}$/.test(account.value)) throw new Error('Enter a valid account ID.')
    actor = await newActor()
    const input = { client_id: data.client_id, target_service: data.target_service, purpose: purpose.value, expected_human_id: account.value, requested_permissions: [{ action: data.action, resource: account.value }] }
    // A create timeout is also uncertain. Lock this page rather than silently
    // create another request on a second click.
    request = { expires_at: 0 }
    const result = await signedFetch(actor, data.origin, 'POST', '/v1/requests', input)
    request = result; account.disabled = true; purpose.disabled = true
    stopped = false; show(result); interval = result.poll_after_seconds
    if (new URL(result.human_authorization_url).origin !== data.origin) throw new Error('Untrusted authorization URL')
    link('Open human review in a separate page', result.human_authorization_url, controls)
    display(result.forwarding_prompt, controls)
    const email = field('Optional invitation email (local outbox only)', 'email', '', controls)
    button('Request one invitation', async () => {
      const id = crypto.randomUUID()
      const result = await signedFetch(actor, data.origin, 'POST', '/v1/requests/' + request.request_id + '/email', { email: email.value, idempotency_key: id })
      say(result.message)
    }, controls)
    button('Cancel this request', async () => { await signedFetch(actor, data.origin, 'POST', '/v1/requests/' + request.request_id + '/cancel', {}); stopped = true; say('Cancelled.') }, controls)
    setTimeout(poll, interval * 1000)
  })
  void start
}
Promise.resolve(({ agent, human, grants, invite }[mode] ?? (() => say('Unknown page')))()).catch(error => say(error.message))
