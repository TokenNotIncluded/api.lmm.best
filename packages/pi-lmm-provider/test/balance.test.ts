import assert from 'node:assert/strict';
import { test } from 'node:test';
import { balanceStatus, parseBalance } from '../src/balance.ts';

const response = { schema_version: 1, currency: 'USD', balance: 1.4, quota: 700000, quota_per_unit: 500000, updated_at: 1789240000, authorization_limit: null };

test('consumes the backend v1 balance fields and labels platform credit truthfully', () => {
  const result = parseBalance(response);
  assert.equal(result.remaining, 1.4);
  assert.equal(balanceStatus(result), 'LMM 1.4 platform credits · wallet only');
  assert.match(balanceStatus(result, true), /stale/);
});

test('accepts the explicit platform credit currency while retaining legacy USD compatibility', () => {
  const result = parseBalance({ ...response, currency: 'platform_credit' });
  assert.equal(result.currency, 'platform_credit');
  assert.equal(balanceStatus(result), 'LMM 1.4 platform credits · wallet only');
});

test('unknown balance is not reported as zero and zero remains a valid balance', () => {
  assert.equal(balanceStatus(parseBalance({ ...response, balance: null })), 'LMM · balance unavailable');
  assert.equal(balanceStatus(parseBalance({ ...response, balance: 0, quota: 0 })), 'LMM 0 platform credits · wallet only');
});

test('rejects inconsistent amounts, wrong schema and unsupported currency', () => {
  for (const patch of [{ balance: 700000 }, { quota_per_unit: 0 }, { quota: 0.5 }, { balance: Infinity }, { schema_version: 2 }, { currency: 'CNY' }, { authorization_limit: 100 }]) {
    assert.throws(() => parseBalance({ ...response, ...patch }));
  }
});
