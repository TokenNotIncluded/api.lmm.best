/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import * as z from 'zod'

import {
  MODERATION_MODELS,
  parseModerationGroupPolicies,
} from './moderation-config'

export const moderationSettingsSchema = z.object({
  ModerationEnabled: z.boolean(),
  ModerationGroup: z.string().trim().min(1).max(64),
  ModerationModel: z.enum(MODERATION_MODELS),
  ModerationGroupPolicies: z
    .string()
    .max(65536)
    .refine(
      (value) => parseModerationGroupPolicies(value) !== null,
      'Use explicit groups, valid modes, and category fees from $0 to $1000 with up to six decimal places.'
    ),
})
export type ModerationSettingsFormValues = z.infer<
  typeof moderationSettingsSchema
>
