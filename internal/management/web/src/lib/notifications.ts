import type { Connection, Delivery } from '../api/types'

export const notificationKinds = ['telegram', 'wecom'] as const
export type NotificationKind = typeof notificationKinds[number]
export const deliveryKind = (delivery: Delivery) => delivery.kind || delivery.channel
export function firstSummaryChannels(deliveries: Delivery[], connections: Connection[]) {
  return notificationKinds.filter(kind =>
    connections.some(connection => connection.kind === kind && connection.credentials_set) &&
    !deliveries.some(delivery => deliveryKind(delivery) === kind && !['skipped', 'disabled', 'not_run', 'inactive'].includes(delivery.state || 'unknown')),
  )
}
export function retryConnections(delivery: Delivery, deliveries: Delivery[], connections: Connection[]) {
  const kind = deliveryKind(delivery)
  if (!delivery.id || !notificationKinds.some(item => item === kind) || !['failed', 'unconfirmed'].includes(delivery.state || '')) return []
  const index = deliveries.findIndex(item => item.id === delivery.id)
  if (index < 0 || deliveries.slice(index + 1).some(item => deliveryKind(item) === kind)) return []
  return connections.filter(connection => connection.kind === kind && connection.credentials_set && connection.id === delivery.connection_id)
}
