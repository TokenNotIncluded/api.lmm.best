import { finite, object, requireValue, unixSeconds } from './protocol.ts';

/** Catalog v1's legacy USD label represents platform wallet units, not fiat. */
export interface Balance {
  currency: 'platform_credit' | 'USD';
  remaining: number | null;
  quota: number;
  quota_per_unit: number;
  updated_at: number;
  authorization_limit: null;
}

export function parseBalance(value: unknown): Balance {
  const row = object(value);
  requireValue(row.schema_version === 1 && (row.currency === 'platform_credit' || row.currency === 'USD') && row.authorization_limit === null);
  const quota = finite(row.quota);
  const quotaPerUnit = finite(row.quota_per_unit);
  requireValue(Number.isSafeInteger(quota) && quotaPerUnit > 0);
  const remaining = row.balance === null ? null : finite(row.balance);
  if (remaining !== null) {
    const expected = quota / quotaPerUnit;
    requireValue(Math.abs(remaining - expected) <= Math.max(1e-9, Math.abs(expected) * 1e-12), 'LMM wallet amounts are inconsistent.');
  }
  return {
    currency: 'platform_credit', remaining, quota, quota_per_unit: quotaPerUnit,
    updated_at: unixSeconds(row.updated_at), authorization_limit: null,
  };
}

export function balanceStatus(balance: Balance | undefined, stale = false): string {
  if (!balance || balance.remaining === null) return 'LMM · balance unavailable';
  const amount = new Intl.NumberFormat('en-US', { maximumFractionDigits: 6 }).format(balance.remaining);
  return `LMM ${amount} platform credits${stale ? ' · stale' : ''} · wallet only`;
}
