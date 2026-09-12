import type { ExtensionAPI, ExtensionContext } from '@earendil-works/pi-coding-agent';
import { getAgentDir } from '@earendil-works/pi-coding-agent';
import { join } from 'node:path';
import { LmmIntegration } from './provider.ts';
import { PROVIDER_ID, boundedSignal, safeMessage } from './protocol.ts';
import { bearerFromHeaders } from './stream.ts';

/** Native Pi provider entry. No account credentials are read from environment variables. */
export default function lmmExtension(pi: ExtensionAPI): void {
  let context: ExtensionContext | undefined;
  let latestStatus: string | undefined;
  const integration = new LmmIntegration({
    refreshJournalDirectory: join(getAgentDir(), 'lmm-refresh-journal'),
    onStatus(status) {
      latestStatus = status;
      if (context?.hasUI) context.ui.setStatus(PROVIDER_ID, status);
    },
  });
  pi.registerProvider(integration.provider);

  pi.on('session_start', async (_event, ctx) => {
    context = ctx;
    if (ctx.hasUI) ctx.ui.setStatus(PROVIDER_ID, latestStatus);
    const result = await ctx.modelRegistry.refresh({
      providers: [PROVIDER_ID], allowNetwork: true, signal: boundedSignal(ctx.signal),
    });
    const error = result.errors.get(PROVIDER_ID);
    if (error && ctx.hasUI) ctx.ui.notify(safeMessage(error), 'warning');
  });

  pi.registerCommand('lmm-prices', {
    description: 'Refresh LMM models and inspect account-specific prices.',
    async handler(filter, ctx) {
      const result = await ctx.modelRegistry.refresh({
        providers: [PROVIDER_ID], allowNetwork: true, force: true, signal: boundedSignal(ctx.signal),
      });
      const error = result.errors.get(PROVIDER_ID);
      pi.sendMessage({
        customType: 'lmm-prices', display: true,
        content: error ? safeMessage(error) : integration.prices(filter.trim()),
      }, { triggerTurn: false });
    },
  });

  pi.registerCommand('lmm-revoke', {
    description: 'Revoke the current LMM server authorization.',
    async handler(_args, ctx) {
      if (!ctx.hasUI) {
        pi.sendMessage({ customType: 'lmm-revoke', display: true, content: 'LMM revoke requires an interactive UI confirmation.' }, { triggerTurn: false });
        return;
      }
      const auth = await ctx.modelRegistry.getProviderAuth(PROVIDER_ID);
      if (!auth?.auth.headers) {
        ctx.ui.notify('No LMM OAuth authorization is configured.', 'warning');
        return;
      }
      if (!await ctx.ui.confirm('Revoke LMM authorization', 'Revoke the current LMM server authorization?')) return;
      try {
        await integration.revoke(bearerFromHeaders(auth.auth.headers), boundedSignal(ctx.signal));
        await ctx.modelRegistry.refresh({ providers: [PROVIDER_ID], allowNetwork: false, signal: boundedSignal(ctx.signal) });
        ctx.ui.notify('LMM server authorization revoked. Use /logout to clear the local login.', 'info');
      } catch (error) {
        ctx.ui.notify(safeMessage(error), 'error');
      }
    },
  });

  pi.on('session_shutdown', () => {
    integration.dispose();
    context = undefined;
  });
}
