/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import { useEffect, useState } from 'react'

export function useDiscountCodeTranslations() {
  const [, setRegistered] = useState(false)
  useEffect(() => {
    let active = true
    void import('./i18n.js').then(({ registerDiscountCodeTranslations }) => {
      registerDiscountCodeTranslations()
      if (active) setRegistered(true)
    })
    return () => {
      active = false
    }
  }, [])
}
