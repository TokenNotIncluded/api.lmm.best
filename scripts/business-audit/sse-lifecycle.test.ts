/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
// Local HTTP protocol tests. This fixture has no billing model or real provider.
import assert from 'node:assert/strict';
import { once } from 'node:events';
import { createServer, type ServerResponse } from 'node:http';
import { setImmediate as nextTurn, setTimeout as delay } from 'node:timers/promises';
import { test, type TestContext } from 'node:test';
import { AssistantStreamError, consumeAssistantAISDKStream, } from '../../apps/web/src/features/assistant/assistant-ai-stream.ts';
const encode = (text: string) => new TextEncoder().encode(text);
const complete = 'event: done\ndata: {"content":"complete"}\n\n';
const partial = 'event: delta\ndata: {"content":"partial"}\n\n';
type Producer = (response: ServerResponse, signal: AbortSignal) => Promise<void>;
async function waitFor(check: () => boolean, message: string) {
  const until = Date.now() + 2000;
  while (!check() && Date.now() < until)
    await delay(5);
  assert.ok(check(), message);
}
function waitForAbort(signal: AbortSignal): Promise<void> {
  if (signal.aborted)
    return Promise.resolve();
  return new Promise((resolve) => {
    signal.addEventListener('abort', () => resolve(), { once: true });
  });
}
async function write(response: ServerResponse, bytes: Uint8Array | string, signal: AbortSignal) {
  signal.throwIfAborted();
  if (!response.write(bytes))
    await once(response, 'drain', { signal });
  await nextTurn();
}
async function localHTTP(t: TestContext, produce: Producer) {
  const state = { requests: 0, responses: 0, tasks: 0, closed: 0 };
  const errors: unknown[] = [];
  const server = createServer((request, response) => {
    state.requests++;
    state.responses++;
    state.tasks++;
    const controller = new AbortController();
    response.once('close', () => {
      state.closed++;
      state.responses--;
      controller.abort();
    });
    request.resume();
    response.writeHead(200, { 'Content-Type': 'text/event-stream' });
    response.flushHeaders();
    void produce(response, controller.signal)
      .catch((error) => {
      if (!controller.signal.aborted)
        errors.push(error);
      response.destroy();
    })
      .finally(() => state.tasks--);
  });
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  t.after(async () => {
    server.closeAllConnections();
    await new Promise<void>((resolve, reject) => {
      server.close((error) => (error ? reject(error) : resolve()));
    });
    assert.deepEqual(errors, []);
  });
  const address = server.address();
  assert.ok(address && typeof address === 'object');
  const request = async () => {
    const response = await fetch(`http://127.0.0.1:${address.port}/chat`, {
      method: 'POST',
      body: '{}',
    });
    assert.equal(response.headers.get('transfer-encoding'), 'chunked');
    assert.ok(response.body);
    return response.body;
  };
  const released = async (body: ReadableStream<Uint8Array>) => {
    assert.equal(body.locked, false);
    await waitFor(() => state.responses === 0 && state.tasks === 0, 'the HTTP response and producer must both stop before fixture teardown');
    assert.equal(state.requests, 1, 'a failed stream must not reconnect or POST again');
    assert.equal(state.closed, 1);
  };
  return { request, released, state };
}
function incomplete(error: unknown): boolean {
  assert.ok(error instanceof AssistantStreamError);
  assert.equal(error.retryable, false);
  assert.match(error.message, /ended before completion|invalid|incomplete/i);
  return true;
}
// Retains the SSE assertion from audit PR #711 (c288ed6c).
test('AUDIT-SSE-01: EOF cannot commit an unterminated success event', async () => {
  const body = new ReadableStream<Uint8Array>({
    start(controller) {
      controller.enqueue(encode('event: done\ndata: {"content":"incomplete-frame"}'));
      controller.close();
    },
  });
  try {
    await assert.rejects(consumeAssistantAISDKStream(body, {}, { timeoutMs: 1000 }), /ended before completion|invalid|incomplete/i);
  }
  finally {
    assert.equal(body.locked, false);
  }
});
test('HTTP EOF never dispatches a pending done or error event', { timeout: 10000 }, async (t) => {
  for (const event of ['done', 'error']) {
    for (const ending of ['', '\n', '\r', '\r\n']) {
      await t.test(`${event} with suffix ${JSON.stringify(ending)}`, async (t) => {
        const fixture = await localHTTP(t, async (response, signal) => {
          await write(response, `event: ${event}\ndata: {"content":"partial","status":503,"retryable":true}${ending}`, signal);
          response.end();
        });
        const body = await fixture.request();
        await assert.rejects(consumeAssistantAISDKStream(body, {}, { timeoutMs: 1000 }), incomplete);
        await fixture.released(body);
      });
    }
  }
});
test('HTTP complete events survive byte-split UTF-8 and all SSE line endings', async (t) => {
  for (const ending of ['\n', '\r', '\r\n']) {
    await t.test(JSON.stringify(ending), async (t) => {
      const text = '你好🌍';
      const fixture = await localHTTP(t, async (response, signal) => {
        const bytes = encode(`: heartbeat${ending}event: delta${ending}data: {"content":"${text}"}${ending}${ending}event: done${ending}data: {"content":"${text}"}${ending}${ending}`);
        for (const byte of bytes)
          await write(response, Uint8Array.of(byte), signal);
        // A valid terminal event must release the connection without HTTP EOF.
        await waitForAbort(signal);
      });
      const body = await fixture.request();
      const deltas: string[] = [];
      assert.deepEqual(await consumeAssistantAISDKStream(body, { onDelta: (value) => deltas.push(value) }, { timeoutMs: 2000 }), { content: text });
      assert.deepEqual(deltas, [text]);
      await fixture.released(body);
    });
  }
});
test('missing or invalid terminal is not success, even with usage or legacy DONE', async (t) => {
  for (const suffix of ['', 'event: usage\ndata: {"total_tokens":12}\n\n', 'data: [DONE]\n\n', 'event: done\ndata: null\n\n', 'event: done\ndata: {broken}\n\n']) {
    await t.test(suffix || 'EOF', async (t) => {
      const fixture = await localHTTP(t, async (response, signal) => {
        await write(response, partial + suffix, signal);
        response.end();
      });
      const body = await fixture.request();
      const deltas: string[] = [];
      await assert.rejects(consumeAssistantAISDKStream(body, { onDelta: (value) => deltas.push(value) }, { timeoutMs: 1000 }), incomplete);
      assert.deepEqual(deltas, ['partial']);
      await fixture.released(body);
    });
  }
});
test('a complete error after partial output releases HTTP and forbids replay', async (t) => {
  const fixture = await localHTTP(t, async (response, signal) => {
    await write(response, partial + 'event: error\ndata: {"status":503,"message":"provider failed","retryable":true}\n\n', signal);
    await waitForAbort(signal);
  });
  const body = await fixture.request();
  await assert.rejects(consumeAssistantAISDKStream(body, {}, { timeoutMs: 1000 }), (error: unknown) => {
    assert.ok(error instanceof AssistantStreamError);
    assert.equal(error.message, 'provider failed');
    assert.equal(error.retryable, false);
    return true;
  });
  await fixture.released(body);
});
test('lost HTTP response is an unknown outcome, not permission to retry', async (t) => {
  const fixture = await localHTTP(t, async (response, signal) => {
    await write(response, partial, signal);
    await delay(20);
    response.destroy();
  });
  const body = await fixture.request();
  await assert.rejects(consumeAssistantAISDKStream(body, {}, { timeoutMs: 1000 }), (error: unknown) => error instanceof AssistantStreamError && !error.retryable);
  await fixture.released(body);
});
test('idle and absolute deadlines stop the actual HTTP producer', async (t) => {
  for (const heartbeat of [false, true]) {
    await t.test(heartbeat ? 'absolute deadline' : 'idle deadline', async (t) => {
      const fixture = await localHTTP(t, async (response, signal) => {
        if (!heartbeat)
          return waitForAbort(signal);
        while (!signal.aborted) {
          await write(response, ': heartbeat\n\n', signal);
          await delay(5);
        }
      });
      const body = await fixture.request();
      await assert.rejects(consumeAssistantAISDKStream(body, {}, { idleTimeoutMs: heartbeat ? 500 : 50, timeoutMs: heartbeat ? 100 : 1000 }), (error: unknown) => error instanceof AssistantStreamError && error.status === 408 && !error.retryable);
      await fixture.released(body);
    });
  }
});
test('slow consumer keeps each delta once and does not require usage in done', async (t) => {
  const fixture = await localHTTP(t, async (response, signal) => {
    for (let i = 0; i < 20; i++)
      await write(response, `event: delta\ndata: {"content":"${i},"}\n\n`, signal);
    await write(response, complete, signal);
    await waitForAbort(signal);
  });
  const body = await fixture.request();
  const slow = body.pipeThrough(new TransformStream<Uint8Array, Uint8Array>({
    async transform(chunk, controller) {
      await delay(10);
      controller.enqueue(chunk);
    },
  }));
  const deltas: string[] = [];
  assert.deepEqual(await consumeAssistantAISDKStream(slow, { onDelta: (value) => deltas.push(value) }, { timeoutMs: 2000 }), { content: 'complete' });
  assert.equal(deltas.join(''), Array.from({ length: 20 }, (_, i) => `${i},`).join(''));
  await waitFor(() => !body.locked, 'pipe cancellation must release the original response');
  await fixture.released(body);
});
test('cancel and terminal races settle once and leave no HTTP producer', { timeout: 10000 }, async (t) => {
  for (let i = 0; i < 12; i++) {
    await t.test(`race ${i}`, async (t) => {
      const fixture = await localHTTP(t, async (response, signal) => {
        await delay(i % 3);
        await write(response, complete, signal);
        await waitForAbort(signal);
      });
      const body = await fixture.request();
      const controller = new AbortController();
      let completions = 0;
      const timer = setTimeout(() => controller.abort(), i % 2);
      try {
        await consumeAssistantAISDKStream(body, {}, { signal: controller.signal, timeoutMs: 1000 }).then((value) => { completions++; assert.deepEqual(value, { content: 'complete' }); }, (error: unknown) => { completions++; assert.ok(error instanceof Error && error.name === 'AbortError'); });
        await fixture.released(body);
        assert.equal(completions, 1);
      }
      finally {
        clearTimeout(timer);
      }
    });
  }
});
test('an HTTP error without an explicit retry receipt is not replayable', () => {
  for (const status of [408, 425, 429, 500, 502, 503, 599]) {
    assert.equal(new AssistantStreamError(status, undefined, 'unknown outcome').retryable, false);
    assert.equal(new AssistantStreamError(status, undefined, 'unknown outcome', 'true' as unknown as boolean).retryable, false);
    assert.equal(new AssistantStreamError(status, { retryable: true }, 'not started', true).retryable, true);
  }
});
test('SSE error status alone cannot authorize another POST', async (t) => {
  for (const retryable of [undefined, 'true']) {
    await t.test(String(retryable), async (t) => {
      const fixture = await localHTTP(t, async (response, signal) => {
        const payload = JSON.stringify({ status: 503, message: 'unknown outcome', retryable });
        await write(response, `event: error\ndata: ${payload}\n\n`, signal);
        await waitForAbort(signal);
      });
      const body = await fixture.request();
      await assert.rejects(consumeAssistantAISDKStream(body, {}, { timeoutMs: 1000 }), (error: unknown) => error instanceof AssistantStreamError && !error.retryable);
      await fixture.released(body);
    });
  }
});
