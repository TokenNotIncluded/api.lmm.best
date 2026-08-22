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
import { useMediaQuery } from '@/hooks/use-media-query'

export const ASSISTANT_RAIL_MIN_WIDTH = 1280
const ASSISTANT_RAIL_MEDIA_QUERY = `(min-width: ${ASSISTANT_RAIL_MIN_WIDTH}px)`

export function isAssistantRailViewport(width: number) {
  return width >= ASSISTANT_RAIL_MIN_WIDTH
}

/**
 * The sidebar's mobile breakpoint is intentionally independent from the
 * assistant presentation breakpoint. Tablet-sized layouts keep the assistant
 * in a sheet so it cannot consume a third column beside the app sidebar.
 */
export function useAssistantOverlay() {
  return !useMediaQuery(ASSISTANT_RAIL_MEDIA_QUERY)
}
