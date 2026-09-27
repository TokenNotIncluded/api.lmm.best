/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/

/** Keep the accounting unit in accessible labels while shortening visible values. */
export function visiblePlatformCredit(value: string, platformLabel: string) {
  const suffix = ` (${platformLabel})`
  return value.endsWith(suffix) ? value.slice(0, -suffix.length) : value
}
