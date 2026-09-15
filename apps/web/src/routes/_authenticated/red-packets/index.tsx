import { createFileRoute, redirect } from '@tanstack/react-router'

import { RedPackets } from '@/features/red-packets'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

// The TanStack Vite plugin regenerates routeTree.gen.ts during production
// builds. Cast here so standalone `tsc` can validate a newly-added file route
// before that generated artifact has been refreshed in the working tree.
export const Route = createFileRoute('/_authenticated/red-packets/' as never)({
  beforeLoad: () => {
    const { auth } = useAuthStore.getState()
    if (!auth.user || auth.user.role < ROLE.ADMIN) {
      throw redirect({ to: '/403' })
    }
  },
  component: RedPackets,
})
