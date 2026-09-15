import { createFileRoute, useParams } from '@tanstack/react-router'

import { RedPacketPublicPage } from '@/features/red-packets/public'

// routeTree.gen.ts is refreshed by the TanStack Vite plugin during builds; the
// cast keeps standalone typecheck compatible with the newly-added file route.
export const Route = createFileRoute('/red-packet/$slug' as never)({
  component: RouteComponent,
})

function RouteComponent() {
  const { slug } = useParams({ strict: false }) as { slug?: string }
  return <RedPacketPublicPage slug={slug ?? ''} />
}
