/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { createFileRoute, redirect } from '@tanstack/react-router'

// Account settings live under Profile. Keep existing settings links usable.
export const Route = createFileRoute('/settings')({
  beforeLoad: () => {
    throw redirect({ to: '/profile', replace: true })
  },
})
