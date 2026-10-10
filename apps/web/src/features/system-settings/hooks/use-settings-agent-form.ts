/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useEffect } from 'react'
import type {
  FieldValues,
  Path,
  PathValue,
  UseFormReturn,
} from 'react-hook-form'
import { toast } from 'sonner'

import appI18n from '@/i18n/config'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import {
  registerSettingsAgentForm,
  type SettingsAgentField,
} from '../utils/settings-agent-bridge'

export function useSettingsAgentForm<T extends FieldValues>(
  form: UseFormReturn<T>,
  fields: readonly SettingsAgentField[]
) {
  const user = useAuthStore((state) => state.auth.user)
  const signature = fields.join(',')
  useEffect(() => {
    if (
      user?.role !== ROLE.SUPER_ADMIN ||
      !signature ||
      !window.location.pathname.startsWith('/system-settings/')
    ) {
      return
    }
    const fields = signature.split(',') as SettingsAgentField[]
    return registerSettingsAgentForm({
      owner: user.id,
      path: window.location.pathname + window.location.search,
      fields,
      read: (name) => form.getValues(name as Path<T>),
      write: (name, value) =>
        form.setValue(name as Path<T>, value as PathValue<T, Path<T>>, {
          shouldDirty: true,
          shouldTouch: true,
        }),
      validate: (names) => form.trigger(names as Path<T>[]),
      isSaving: () => form.formState.isSubmitting,
      changed: (name) => form.getFieldState(name as Path<T>).isDirty,
      notify: () =>
        toast.info(
          appI18n.t(
            'Settings draft updated. Review it and press Save to apply.'
          )
        ),
    })
  }, [form, signature, user?.id, user?.role])
}
