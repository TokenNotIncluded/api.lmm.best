/*
Copyright (C) 2026 LIghtJUNction
SPDX-License-Identifier: AGPL-3.0-or-later
*/
import { AssistantSupportReviewDialog } from './assistant-support-review-dialog'

// Share the reviewed form rather than maintaining two confirmation paths.
export function AssistantSupportReview() {
  return (
    <div className='px-3 pb-3'>
      <AssistantSupportReviewDialog />
    </div>
  )
}
