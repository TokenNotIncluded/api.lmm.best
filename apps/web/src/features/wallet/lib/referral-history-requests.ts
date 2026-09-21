/*
Copyright (C) 2026 LIghtJUNction
*/
export type ReferralHistoryRequest = {
  readonly before: number
  readonly controller: AbortController
}

// Keep request ownership outside React's asynchronous render cycle. Closing a
// dialog detaches its request before abort listeners or late promises can run.
export function createReferralHistoryRequests() {
  let active: ReferralHistoryRequest | null = null
  return {
    begin(before: number): ReferralHistoryRequest | null {
      if (active) return null
      active = { before, controller: new AbortController() }
      return active
    },
    isCurrent(request: ReferralHistoryRequest): boolean {
      return active === request && !request.controller.signal.aborted
    },
    finish(request: ReferralHistoryRequest): boolean {
      if (active !== request) return false
      active = null
      return true
    },
    cancel(): void {
      const request = active
      active = null
      request?.controller.abort()
    },
  }
}
