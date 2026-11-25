import { apiClient } from './client'
import type { UserSubscription } from '@/api/subscriptions.types'

/**
 * subscriptions.ts retrieves the signed-in user's subscription lists and submits
 * spending-order changes and exhausted-subscription revocation. The revocation
 * result exposes the revoked record, any successor and rebound-key count,
 * allowing callers to refresh subscription and API-key state after the operation.
 */

/**
 * RevokeSubscriptionResponse identifies the revoked subscription, any selected
 * successor and the number of API keys rebound by the server.
 */
export interface RevokeSubscriptionResponse {
  revoked_subscription_id: number // Revoked subscription ID.
  replacement_subscription_id: number | null // Replacement subscription ID.
  rebound_api_key_count: number // Rebound API key count.
}

/**
 * getMySubscriptions retrieves list of current user's subscriptions and returns
 * the server result.
 */
export async function getMySubscriptions(): Promise<UserSubscription[]> {
  const response = await apiClient.get<UserSubscription[]>('/subscriptions')
  return response.data
}

/**
 * getActiveSubscriptions retrieves current user's active subscriptions and
 * returns the server result.
 */
export async function getActiveSubscriptions(): Promise<UserSubscription[]> {
  const response = await apiClient.get<UserSubscription[]>('/subscriptions/active')
  return response.data
}

/**
 * revokeExhaustedSubscription requests revocation of the current user's
 * exhausted subscription. The response includes a successor and rebound-key
 * count when the server transfers keys with explicit subscription bindings to a
 * continuation package.
 */
export async function revokeExhaustedSubscription(
  subscriptionId: number,
): Promise<RevokeSubscriptionResponse> {
  const response = await apiClient.post<RevokeSubscriptionResponse>(
    `/subscriptions/${subscriptionId}/revoke`,
  )
  return response.data
}

/**
 * reorderSubscriptions saves the current user's automatic consumption order.
 * Every ID identifies an owned eligible subscription; the server validates the
 * complete request before persisting any rank and appends omitted grants.
 */
export async function reorderSubscriptions(subscriptionIds: number[]): Promise<void> {
  await apiClient.put('/subscriptions/order', { subscription_ids: subscriptionIds })
}

/**
 * SubscriptionResetCard describes one included purchase benefit and its history.
 * An unused card funds only its originating paid grant before expiry; a used
 * card retains its credited amount and original-week expiry for display.
 */
export interface SubscriptionResetCard {
  id: number // Issued benefit identity.
  plan_id: number // Exact eligible subscription plan.
  source_subscription_id: number // Original paid subscription identity.
  plan_name: string // Current product display name.
  limit_usd: number // USD per-card restoration cap.
  expires_at: string // Exclusive card expiry.
  used_at?: string // Successful redemption timestamp.
  target_subscription_id?: number // Redeemed originating subscription identity.
  credited_usd: number // USD restored at redemption.
  remaining_usd: number // USD unreserved current-week welfare balance.
  benefit_expires_at?: string // Exclusive original-week benefit expiry.
  status: 'available' | 'used' | 'expired' | 'unavailable' // Effective inventory state.
}

/** getResetCards retrieves the current user's automatic purchase benefits and history. */
export async function getResetCards(): Promise<SubscriptionResetCard[]> {
  return (await apiClient.get<SubscriptionResetCard[]>('/subscriptions/reset-cards')).data
}

/**
 * useResetCard consumes one owned card for its originating paid subscription.
 * Repeating the same card and target returns its original credited result;
 * the server validates availability and settled spending before any mutation.
 */
export async function useResetCard(
  cardId: number,
  subscriptionId: number,
): Promise<SubscriptionResetCard> {
  return (
    await apiClient.post<SubscriptionResetCard>(`/subscriptions/reset-cards/${cardId}/use`, {
      subscription_id: subscriptionId,
    })
  ).data
}

export default {
  getResetCards,
  useResetCard,
  reorderSubscriptions,
  getMySubscriptions,
  getActiveSubscriptions,
  revokeExhaustedSubscription,
}
