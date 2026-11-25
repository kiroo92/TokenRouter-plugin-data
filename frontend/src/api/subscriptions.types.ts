import type { User } from '@/api/identity.types'
import type { SubscriptionPlan } from '@/api/payment.types'

// subscriptions.types.ts describes purchased plan assignments and their spending windows.
// Subscription records include daily, weekly, and monthly allowances, usage,
// and a persistent consumption priority. Repeated purchases retain separate
// identities, while administrative requests grant new subscriptions or change
// the validity of a selected existing assignment.

// UserSubscription records an account's purchased plan and its current allowance windows.
export interface UserSubscription {
  id: number // Record numeric identifier.
  user_id: number // Owning account identifier.
  plan_id: number // Subscription plan identifier.
  // Older servers may omit consumption priority. Consumers retain server list
  // order in that case; current servers include a rank for every grant.
  consumption_priority?: number // Stored automatic consumption rank.
  starts_at: string // Activation start timestamp.
  expires_at: string // Record expiration timestamp.
  status: 'active' | 'pending' | 'expired' | 'suspended' | 'revoked' // Current lifecycle state.
  reset_credit_usd?: number // USD welfare balance expiring at the current week boundary.
  daily_limit_usd: number | null // Daily limit USD.
  weekly_limit_usd: number | null // Weekly limit USD.
  monthly_limit_usd: number | null // Monthly limit USD.
  daily_usage_usd: number // Daily usage USD.
  weekly_usage_usd: number // Weekly usage USD.
  monthly_usage_usd: number // Monthly usage USD.
  daily_window_start: string | null // Daily window start.
  weekly_window_start: string | null // Weekly window start.
  monthly_window_start: string | null // Monthly window start.
  created_at: string // Record creation timestamp.
  updated_at: string // Record update timestamp.
  revoked_at?: string | null // Subscription revocation timestamp.
  user?: User // Expanded account details.
  plan?: SubscriptionPlan // Expanded purchased subscription plan.
}

// AssignSubscriptionRequest selects the account and plan for an administrator
// subscription grant.
export interface AssignSubscriptionRequest {
  user_id: number // Owning account identifier.
  plan_id: number // Subscription plan identifier.
  validity_days?: number // Granted validity days.
  notes?: string // Administrator record notes.
}

// ExtendSubscriptionRequest adds the specified number of days to a subscription.
export interface ExtendSubscriptionRequest {
  days: number // Subscription extension days.
}
