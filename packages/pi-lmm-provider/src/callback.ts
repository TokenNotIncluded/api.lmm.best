import { timingSafeEqual } from 'node:crypto';
import { createServer, type IncomingMessage, type ServerResponse } from 'node:http';
import { CALLBACK_PATH, LmmError } from './protocol.ts';

function equal(left: string | null, right: string): boolean {
  if (left === null || left.length > 4096) return false;
  const a = Buffer.from(left);
  const b = Buffer.from(right);
  return a.length === b.length && timingSafeEqual(a, b);
}

function reply(response: ServerResponse, status: number, message: string): void {
  response.writeHead(status, {
    'content-type': 'text/plain; charset=utf-8',
    'cache-control': 'no-store', 'referrer-policy': 'no-referrer',
    'content-security-policy': "default-src 'none'; frame-ancestors 'none'", 'x-content-type-options': 'nosniff',
    connection: 'close',
  });
  response.end(message);
}

/** Bind before returning a redirect URI. Invalid/error callbacks never settle the login. */
export async function listenCallback(issuer: string, state: string, signal: AbortSignal): Promise<{
  redirectUri: string;
  code: Promise<string>;
  close(): void;
}> {
  signal.throwIfAborted();
  let accept: (code: string) => void = () => {};
  let reject: (error: Error) => void = () => {};
  const code = new Promise<string>((resolve, fail) => { accept = resolve; reject = fail; });
  void code.catch(() => {}); // The owner starts awaiting after the browser notification.
  let authority = '';
  let settled = false;
  let closed = false;
  const server = createServer({ maxHeaderSize: 8192 }, (request: IncomingMessage, response) => {
    if (settled || request.method !== 'GET' || request.headers.host !== authority ||
        request.socket.remoteAddress !== '127.0.0.1' || request.url?.split('?')[0] !== CALLBACK_PATH) {
      reply(response, 400, 'Invalid OAuth callback. Return to Pi to cancel or retry.');
      return;
    }
    const url = new URL(request.url, `http://${authority}`);
    const params = url.searchParams;
    const valid = ['state', 'iss', 'code'].every((key) => params.getAll(key).length === 1) &&
      [...params.keys()].every((key) => ['state', 'iss', 'code'].includes(key)) &&
      equal(params.get('state'), state) && params.get('iss') === issuer;
    const returnedCode = params.get('code');
    const errorKeys = ['state', 'iss', 'error'];
    const hasError = params.has('error');
    const errorValid = errorKeys.every((key) => params.getAll(key).length === 1) &&
      [...params.keys()].every((key) => errorKeys.includes(key) || key === 'error_description' || key === 'error_uri') &&
      params.getAll('error_description').length <= 1 && params.getAll('error_uri').length <= 1 &&
      equal(params.get('state'), state) && params.get('iss') === issuer &&
      /^[A-Za-z0-9._~-]{1,256}$/.test(params.get('error') ?? '');
    if (hasError && errorValid && !params.has('code')) {
      settled = true;
      reply(response, 200, 'Authorization was not granted. Return to Pi.');
      reject(new LmmError('oauth_denied', 'LMM authorization was denied.'));
      return;
    }
    if (!valid || returnedCode === null || !/^[A-Za-z0-9._~-]{1,4096}$/.test(returnedCode)) {
      reply(response, 400, 'Unverified OAuth callback. The login is still waiting in Pi.');
      return;
    }
    settled = true;
    reply(response, 200, 'Authorization received. Return to Pi to finish signing in.');
    accept(returnedCode);
  });
  server.requestTimeout = 5000;
  server.headersTimeout = 5000;
  server.keepAliveTimeout = 1;

  const close = () => {
    if (closed) return;
    closed = true;
    signal.removeEventListener('abort', abort);
    server.close();
    server.closeAllConnections();
    if (!settled) {
      settled = true;
      reject(new LmmError('aborted', 'LMM login cancelled or timed out.'));
    }
  };
  const abort = () => close();
  signal.addEventListener('abort', abort, { once: true });
  try {
    await new Promise<void>((resolve, fail) => {
      const onError = () => fail(new LmmError('callback_unavailable', 'Could not listen on the local OAuth callback address.'));
      server.once('error', onError);
      server.listen(0, '127.0.0.1', () => {
        server.removeListener('error', onError);
        resolve();
      });
    });
    // If cancellation raced the listen callback, close the newly bound listener too.
    if (signal.aborted) {
      server.close();
      server.closeAllConnections();
      throw new LmmError('aborted', 'LMM login cancelled or timed out.');
    }
    server.on('error', () => {
      reject(new LmmError('callback_unavailable', 'The local OAuth callback listener failed.'));
      close();
    });
    const address = server.address();
    if (!address || typeof address === 'string') throw new LmmError('callback_unavailable', 'No local OAuth callback port was allocated.');
    authority = `127.0.0.1:${address.port}`;
    return { redirectUri: `http://${authority}${CALLBACK_PATH}`, code, close };
  } catch (error) {
    close();
    throw error;
  }
}
