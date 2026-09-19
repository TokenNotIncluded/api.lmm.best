export function costAmountMicros(value: string) {
  if (!/^\d{1,9}(?:\.\d{1,6})?$/.test(value)) return null
  const [whole, fraction = ''] = value.split('.')
  const result = Number(whole) * 1_000_000 + Number(fraction.padEnd(6, '0'))
  return Number.isSafeInteger(result) ? result : null
}
