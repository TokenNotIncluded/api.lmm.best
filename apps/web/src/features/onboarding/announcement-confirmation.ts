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
type AnnouncementRevision = {
  id: number
  revision: string
  read_at: number
}

/** Confirm only the revisions that were visible when the user clicked. */
export async function confirmVisibleAnnouncements<T extends AnnouncementRevision>(
  visible: readonly T[],
  acknowledge: (item: T) => Promise<readonly T[]>
): Promise<void> {
  const revisionKey = (item: T) => `${item.id}:${item.revision}`
  const allowed = new Set(
    visible.filter((item) => !item.read_at).map(revisionKey)
  )
  const confirmed = new Set<string>()
  let snapshot = visible

  while (true) {
    // Follow the server's current order, not a locally guessed queue.
    const next = snapshot.find((item) => !item.read_at)
    if (!next) return
    const key = revisionKey(next)
    // New or edited publications need a new, explicit confirmation.
    if (!allowed.has(key)) return
    if (confirmed.has(key)) throw new Error('Unable to confirm reading')
    confirmed.add(key)
    snapshot = await acknowledge(next)
  }
}
