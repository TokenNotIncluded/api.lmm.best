/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/

const ITEM_LABELS: Record<string, string> = {
  'assistant.human_support': 'Human technical support',
  'open_source_bounty.challenge_submitted': 'Challenge submitted',
  'open_source_bounty.tip_received': 'Tip received',
  'open_source_bounty.reward_received': 'Reward received',
  'open_source_bounty.dispute_reward_received': 'Dispute reward received',
  'open_source_bounty.notification': 'Bounty notification',
  'developer_access.request': 'Developer access request',
  'account_action.request': 'Account action request',
  'assistant.security_incident': 'Assistant safety incident',
  'assistant.security_review': 'assistant.security_review',
}

export function todoItemTitleKey(title: string) {
  return ITEM_LABELS[title] ?? 'Notification'
}
