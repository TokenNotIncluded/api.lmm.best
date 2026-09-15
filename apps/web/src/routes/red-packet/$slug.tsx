import { createFileRoute } from '@tanstack/react-router'

import { RedPacketPublicPage } from '@/features/red-packets/public'

export const Route = createFileRoute('/red-packet/$slug')({
  component: RouteComponent,
})

function RouteComponent() {
  const { slug } = Route.useParams()
  return <RedPacketPublicPage slug={slug} />
}
