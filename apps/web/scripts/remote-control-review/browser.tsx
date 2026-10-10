// A loopback test page using the production controls, command hook and encryption.
import React, { useEffect, useState } from 'react'
import { createRoot } from 'react-dom/client'
import i18next from 'i18next'
import { initReactI18next } from 'react-i18next'
import { RemoteComposer, RemoteQuestionCard } from '../../src/features/remote-control/controls'
import { useRemoteCommands } from '../../src/features/remote-control/use-remote-commands'
import { derivePiRemoteKey, decryptPiRemoteMessage, decryptPiSessionMetadata } from '../../src/features/remote-control/crypto'
import { normalizePiMessageEnvelopes, normalizePiSessionEnvelopes } from '../../src/features/remote-control/protocol'
import { latestRemoteState, remoteConversation } from '../../src/features/remote-control/commands'
import type { RemoteControlMessage, PiRemoteSessionEnvelope } from '../../src/features/remote-control/types'
import { remoteControlCopy } from '../remote-control-copy.mjs'
await i18next.use(initReactI18next).init({ lng: 'zh', fallbackLng: 'en', resources: { zh: { translation: remoteControlCopy.zh }, en: { translation: remoteControlCopy.en } }, interpolation: { escapeValue: false } })
const t = i18next.t.bind(i18next)
const headers = { authorization: 'Bearer lmm_at_fixture' }
function Workspace({ envelope, sessionKey, lock }: { envelope: PiRemoteSessionEnvelope; sessionKey: CryptoKey; lock: () => void }) {
  const [messages, setMessages] = useState<RemoteControlMessage[]>([])
  useEffect(() => {
    let disposed = false
    let timer: ReturnType<typeof setTimeout>
    const abort = new AbortController()
    async function poll() {
      try {
        const response = await fetch(`/api/remote-control/v1/pi/sessions/${envelope.sessionId}/messages?after=0`, { headers, signal: abort.signal })
        const body = await response.json()
        const result = await Promise.all(normalizePiMessageEnvelopes(body.data).filter(message => message.sender === 'plugin').map(message => decryptPiRemoteMessage(envelope.sessionId, message, sessionKey)))
        if (!disposed) setMessages(result)
      } finally { if (!disposed) timer = setTimeout(() => void poll(), 300) }
    }
    void poll()
    return () => { disposed = true; abort.abort(); clearTimeout(timer) }
  }, [envelope.sessionId, sessionKey])
  const { send, pending } = useRemoteCommands(envelope.sessionId, sessionKey, messages)
  const state = latestRemoteState(messages)
  return <><header><div><h1>Pi 远程控制</h1><p data-testid='provider'>{state?.provider} / {state?.model}</p></div><button onClick={lock}>锁定</button></header>
    {(state?.requests ?? []).map(question => <RemoteQuestionCard key={question.request_id} question={question} send={send} disabled={pending} />)}
    <RemoteComposer send={send} disabled={!state} busy={!!state?.busy} hasQuestion={!!state?.requests?.length} />
    <section aria-label='会话记录'>{remoteConversation(messages).map((message, index) => <pre key={message.id ?? index}>{message.content}</pre>)}</section></>
}
function App() {
  const [envelope, setEnvelope] = useState<PiRemoteSessionEnvelope>()
  const [pin, setPin] = useState('')
  const [key, setKey] = useState<CryptoKey>()
  const [error, setError] = useState('')
  useEffect(() => { void fetch('/api/remote-control/v1/pi/sessions', { headers }).then(r => r.json()).then(body => setEnvelope(normalizePiSessionEnvelopes(body.data)[0])) }, [])
  if (envelope && key) return <Workspace envelope={envelope} sessionKey={key} lock={() => setKey(undefined)} />
  return <form onSubmit={async event => { event.preventDefault(); if (!envelope) return; try { const next = await derivePiRemoteKey(pin, envelope.sessionId); await decryptPiSessionMetadata(envelope, next); setKey(next); setPin(''); setError('') } catch { setError('PIN 不正确') } }}><h1>解锁 Pi 会话</h1><p>使用任何模型。PIN 只用于本地解密。</p><label>PIN<input aria-label='PIN' type='password' value={pin} onChange={e => setPin(e.target.value)} /></label><button disabled={!envelope || !pin}>解锁</button>{error ? <p role='alert'>{error}</p> : null}</form>
}
createRoot(document.getElementById('root')!).render(<App />)
