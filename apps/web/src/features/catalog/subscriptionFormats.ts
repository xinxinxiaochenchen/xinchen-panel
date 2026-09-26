export type SubscriptionFormat = 'clash' | 'mihomo' | 'sing-box' | 'surge'

export const subscriptionFormats: { value: SubscriptionFormat; label: string }[] = [
  { value: 'clash', label: 'Clash' },
  { value: 'mihomo', label: 'Mihomo' },
  { value: 'sing-box', label: 'sing-box' },
  { value: 'surge', label: 'Surge' },
]

export function subscriptionURLPath(id: string, format: SubscriptionFormat): string {
  return `/api/v1/subscriptions/${encodeURIComponent(id)}/url?format=${format}`
}

export function subscriptionPreviewPath(id: string, format: SubscriptionFormat): string {
  return `/api/v1/subscriptions/${encodeURIComponent(id)}/preview?format=${format}`
}

export function subscriptionTokenPath(token: string, format: SubscriptionFormat): string {
  return `/sub/${encodeURIComponent(token)}/${format}`
}
