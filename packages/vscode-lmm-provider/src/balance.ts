import { assert, record } from "./protocol";

function finite(value: unknown): number {
  assert(typeof value === "number" && Number.isFinite(value));
  return value;
}
function decimal(value: unknown): number {
  assert(typeof value === "string" && value.length <= 80 && /^(?:0|[1-9]\d*)(?:\.\d+)?$/.test(value));
  const number = Number(value);
  assert(Number.isFinite(number) && number > 0);
  return number;
}
/** Same wallet-unit validation as the LMM Pi provider; never treat quota as dollars. */
export function balanceLabel(value: unknown): string {
  const row = record(value);
  assert((row.schema_version === 1 || row.schema_version === 2) && row.authorization_limit === null);
  assert(row.schema_version === 2 ? row.currency === "USD" : row.currency === "platform_credit" || row.currency === "USD");
  const quota = finite(row.quota), perUnit = finite(row.quota_per_unit);
  assert(Number.isSafeInteger(quota) && perUnit > 0);
  assert(Number.isSafeInteger(row.updated_at) && row.updated_at >= 0);
  const balance = row.balance === null ? null : finite(row.balance);
  if (balance !== null) {
    const expected = quota / perUnit;
    assert(Number.isFinite(expected) && Math.abs(balance - expected) <= Math.max(1e-9, Math.abs(expected) * 1e-12));
  }
  if (row.schema_version === 1) {
    // V1 has no verified USD basis, even when its legacy currency says USD.
    return balance === null ? "余额不可用" : `${format(balance)} 平台额度（旧版单位）`;
  }
  assert(balance !== null && row.credit_unit === 1 && decimal(row.credits_per_usd) === perUnit);
  assert(typeof row.public_credit_balance === "string" && row.public_credit_balance.length <= 80 && /^(?:0|-?[1-9]\d*)$/.test(row.public_credit_balance));
  assert(Number(row.public_credit_balance) === quota);
  const keys = ["credit_unit_schema_version", "quota_unit", "public_credit_unit", "legacy_credit_unit", "ledger_quota_per_usd", "ledger_quota_per_usd_exact", "public_credits_per_usd", "public_credits_per_usd_exact"];
  if (keys.some((key) => key in row)) {
    assert(row.credit_unit_schema_version === 2 && row.quota_unit === "LEDGER_QUOTA" && row.public_credit_unit === "CREDIT" && row.legacy_credit_unit === "LEDGER_QUOTA");
    assert(finite(row.ledger_quota_per_usd) === perUnit && finite(row.public_credits_per_usd) === perUnit && decimal(row.ledger_quota_per_usd_exact) === perUnit && decimal(row.public_credits_per_usd_exact) === perUnit);
  }
  return `${format(balance)} USD · ${format(quota)} 平台额度`;
}
function format(value: number): string {
  return new Intl.NumberFormat("en-US", { maximumSignificantDigits: 12 }).format(value);
}
