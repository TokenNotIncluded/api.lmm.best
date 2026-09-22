/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
type FilterValue = string | number | boolean

/** Ignore malformed filter state instead of constructing a Set from an object. */
export function getFilterValues(value: unknown): FilterValue[] {
  const values = Array.isArray(value) ? value : [value]
  return values.filter(
    (item): item is FilterValue =>
      (typeof item === 'string' && item !== '') ||
      (typeof item === 'number' && Number.isFinite(item)) ||
      typeof item === 'boolean'
  )
}

export function removeFilterValue(current: unknown, value: FilterValue) {
  const remaining = getFilterValues(current).filter((item) => item !== value)
  return remaining.length ? remaining : undefined
}
