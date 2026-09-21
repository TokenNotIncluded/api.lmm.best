#!/usr/bin/env node
import { parseArgs } from 'node:util';
import { spawn } from 'node:child_process';
import { pathToFileURL } from 'node:url';
import { OAuth, check, safeMessage } from './oauth.mjs';
import { SessionStore } from './session.mjs';
import { catalog, runCodewhale } from './provider.mjs';

const help = `LMM OAuth adapter for Codewhale (Node.js 22+)\n\n  codewhale-lmm login [--no-browser]\n  codewhale-lmm models\n  codewhale-lmm run --model <catalog-id> [-- <codewhale arguments>]\n  codewhale-lmm status | balance | usage\n  codewhale-lmm logout [--local-only]\n  codewhale-lmm unlock\n\nOptions: --issuer <https-origin>\nEnvironment: LMM_ISSUER, LMM_CODEWHALE_HOME, LMM_CODEWHALE_BIN\n\nThis is a companion adapter, not a native /login provider hook.\nThe current custom-provider route supports Chat Completions models only.\n`;

export async function openBrowser(url, noBrowser = false) {
  console.error(`Approve LMM in your browser:\n${url}\n`);
  if (noBrowser) return;
  const [command, args] = process.platform === 'darwin' ? ['open', [url]]
    : process.platform === 'win32' ? ['rundll32.exe', ['url.dll,FileProtocolHandler', url]]
    : process.env.TERMUX_VERSION || process.env.PREFIX?.includes('com.termux') ? ['termux-open-url', [url]]
    : ['xdg-open', [url]];
  // Browser opening is best effort. The printed URL remains usable without a GUI opener.
  await new Promise(resolve => {
    const child = spawn(command, args, { stdio: 'ignore', shell: false });
    child.once('error', resolve);
    child.once('spawn', () => { child.unref(); resolve(); });
  });
}

export async function main(argv = process.argv.slice(2), signal) {
  const split = argv.indexOf('--');
  const own = split < 0 ? argv : argv.slice(0, split);
  const forwarded = split < 0 ? [] : argv.slice(split + 1);
  const { values, positionals } = parseArgs({ args: own, allowPositionals: true, options: {
    issuer: { type: 'string' }, model: { type: 'string' }, 'no-browser': { type: 'boolean' },
    'local-only': { type: 'boolean' }, help: { type: 'boolean', short: 'h' },
  } });
  check(positionals.length <= 1, 'Unexpected arguments. Use -- before Codewhale arguments.');
  const command = values.help ? 'help' : positionals[0] || 'help';
  check(['help', 'login', 'logout', 'models', 'status', 'balance', 'usage', 'run', 'unlock'].includes(command), 'Unknown command. Run codewhale-lmm --help.');
  check(!values['local-only'] || command === 'logout', '--local-only is only valid with logout.');
  check(!values['no-browser'] || command === 'login', '--no-browser is only valid with login.');
  check(!values.model || command === 'run', '--model is only valid with run.');
  check(!forwarded.length || command === 'run', 'Only run accepts Codewhale arguments.');
  check(!forwarded.some(arg => /^--(?:provider|model|base-url|api-key|config|config-path)(?:=|$)/.test(arg)), 'Do not override provider/model/credentials/config after --; choose the LMM model with --model.');
  if (command === 'help') { console.log(help); return 0; }
  const store = new SessionStore(new OAuth(values.issuer || process.env.LMM_ISSUER));
  const output = value => console.log(JSON.stringify(value, null, 2));
  switch (command) {
    case 'login':
      await store.login(url => openBrowser(url, values['no-browser']), AbortSignal.any([AbortSignal.timeout(180_000), ...(signal ? [signal] : [])]));
      console.log('LMM login saved. Run codewhale-lmm models, then run --model <ID>.');
      break;
    case 'logout':
      await store.logout(values['local-only'], signal);
      console.log(values['local-only'] ? 'Local credentials deleted; server authorization was NOT revoked.' : 'LMM authorization revoked and local credentials deleted.');
      break;
    case 'status': output(await store.status(signal)); break;
    case 'models': {
      const { models } = await catalog(store, signal);
      output(models.map(({ id, name, group, upstream_model, pricing }) => ({ id, name, group, upstream_model, pricing })));
      break;
    }
    case 'balance': case 'usage': {
      const session = await store.access(signal);
      output(await store.oauth.request(command === 'balance' ? '/api/oauth2/balance' : '/api/oauth2/usage/activity', { access: session.access, signal }));
      break;
    }
    case 'run': return runCodewhale(store, values.model, forwarded, { signal, binary: process.env.LMM_CODEWHALE_BIN || 'codewhale' });
    case 'unlock': await store.unlock(); console.log('No stale LMM session lock remains.'); break;
  }
  return 0;
}
if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const controller = new AbortController();
  const stop = () => controller.abort();
  process.once('SIGINT', stop);
  process.once('SIGTERM', stop);
  try { process.exitCode = await main(undefined, controller.signal); }
  catch (error) { console.error(safeMessage(error)); process.exitCode = controller.signal.aborted ? 130 : 1; }
  finally { process.removeListener('SIGINT', stop); process.removeListener('SIGTERM', stop); }
}
