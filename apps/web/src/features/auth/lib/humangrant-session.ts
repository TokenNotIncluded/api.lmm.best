/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */

// Local request ownership, not server authorization. HumanGrant still requires
// the server's human cookie and CSRF proof; never copy a dashboard Bearer token.
export type HumanGrantIdentity = Readonly<{
  userId: number | undefined
  sessionId: string | undefined
}>

export function createHumanGrantSessionScope(
  identity: HumanGrantIdentity,
  currentIdentity: () => HumanGrantIdentity,
  fetcher: typeof fetch = fetch
) {
  // Copy scalar values. A caller mutating its object cannot retarget this scope.
  const { userId, sessionId } = identity
  let invalidated = false

  function check(): void {
    const current = currentIdentity()
    if (
      invalidated ||
      !Number.isSafeInteger(userId) ||
      userId === undefined ||
      userId <= 0 ||
      typeof sessionId !== 'string' ||
      sessionId.length === 0 ||
      current.userId !== userId ||
      current.sessionId !== sessionId
    ) {
      invalidated = true
      throw new DOMException('HumanGrant session changed', 'AbortError')
    }
  }

  function checkOrCancel(response: Response): void {
    try {
      check()
    } catch (error) {
      // Drop a late response without reading its CSRF proof or account data.
      void response.body?.cancel().catch(() => {})
      throw error
    }
  }

  const scopedFetch: typeof fetch = async (input, init) => {
    check()
    const response = await fetcher(input, init)
    checkOrCancel(response)
    const json = response.json.bind(response)
    Object.defineProperty(response, 'json', {
      configurable: true,
      value: async () => {
        checkOrCancel(response)
        const value: unknown = await json()
        checkOrCancel(response)
        return value
      },
    })
    return response
  }

  return {
    check,
    fetch: scopedFetch,
    invalidate() {
      invalidated = true
    },
  }
}
