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
import {
  CircleAlert,
  CircleCheck,
  CircleHelp,
  CirclePause,
  type LucideIcon,
} from 'lucide-react'

import { CHANNEL_STATUS } from '../constants'

/**
 * Shape that pairs with each channel status colour.
 *
 * DESIGN.md requires status to be readable without colour alone, so the
 * badge always renders one of these next to its label. Kept in `lib/` so
 * non-component modules (and tests) can resolve a status icon without
 * pulling in React components.
 */
export const CHANNEL_STATUS_ICON_MAP: Record<number, LucideIcon> = {
  [CHANNEL_STATUS.UNKNOWN]: CircleHelp,
  [CHANNEL_STATUS.ENABLED]: CircleCheck,
  [CHANNEL_STATUS.MANUAL_DISABLED]: CirclePause,
  [CHANNEL_STATUS.AUTO_DISABLED]: CircleAlert,
}

/** Resolve the status icon, falling back to the unknown icon. */
export function getChannelStatusIcon(status: number): LucideIcon {
  return CHANNEL_STATUS_ICON_MAP[status] ?? CircleHelp
}
