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
/*
Copyright (C) 2026 LIghtJUNction
*/
import type { RedPacketPublic } from './types'

/** Use state identifiers, not translated text, for action eligibility. */
export function redPacketStatus(packet: RedPacketPublic, now: number) {
  if (!packet.enabled) return 'Paused'
  if (packet.end_at > 0 && packet.end_at <= now) return 'Ended'
  if (packet.remaining_items <= 0) return 'Exhausted'
  if (packet.start_at > now) return 'Scheduled'
  return 'Live'
}

export function canDeleteRedPacket(packet: RedPacketPublic, now: number) {
  return ['Paused', 'Ended', 'Exhausted'].includes(redPacketStatus(packet, now))
}
