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
import type { DeveloperAccessRequest } from '@/features/onboarding/api'

import type { AssistantL1RecommendationAction } from './api'
import { AssistantRegistrationStatus } from './assistant-registration-status'

// Old draft props remain source-compatible while historical conversations are
// opened. They cannot submit, confirm or silently replay an application letter.
export function AssistantActivationTool(props: {
  recommendationDraft?: AssistantL1RecommendationAction | null
  onContinueSetup?: () => void
  onSubmitted?: (request: DeveloperAccessRequest) => void
  onDraftConsumed?: () => void
  onApproved?: () => void
}) {
  return (
    <AssistantRegistrationStatus
      onContinueSetup={props.onContinueSetup}
      onApproved={props.onApproved}
    />
  )
}
