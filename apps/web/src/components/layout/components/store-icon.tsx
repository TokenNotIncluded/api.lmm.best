/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import { Store01Icon } from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import type { ComponentProps } from 'react'

export function StoreIcon(
  props: Omit<ComponentProps<typeof HugeiconsIcon>, 'icon'>
) {
  return <HugeiconsIcon icon={Store01Icon} strokeWidth={1.8} {...props} />
}
