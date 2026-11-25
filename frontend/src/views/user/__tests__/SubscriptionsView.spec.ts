import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createI18n } from 'vue-i18n'
import { createRouter, createMemoryHistory } from 'vue-router'
import SubscriptionsView from '../SubscriptionsView.vue'

// SubscriptionsView.spec.ts checks independent subscription cards and exhausted-subscription revocation
// through the production page. Real stores and routing surround mocked subscription
// requests, while a controllable confirmation shell verifies eligibility, required
// confirmation, and duplicate-submit prevention.

// Subscription request mocks independently control list retrieval, active-store refresh, and
// the exhausted-subscription mutation used by confirmation scenarios.
const mockGetMySubscriptions = vi.fn()
const mockGetActiveSubscriptions = vi.fn()
const mockRevokeExhaustedSubscription = vi.fn()
const mockReorderSubscriptions = vi.fn()
const mockGetResetCards = vi.fn()
const mockUseResetCard = vi.fn()

vi.mock('@/api/subscriptions', () => ({
  default: {
    getResetCards: (...args: unknown[]) => mockGetResetCards(...args),
    useResetCard: (...args: unknown[]) => mockUseResetCard(...args),
    getMySubscriptions: (...args: unknown[]) => mockGetMySubscriptions(...args),
    reorderSubscriptions: (...args: unknown[]) => mockReorderSubscriptions(...args),
    getActiveSubscriptions: (...args: unknown[]) => mockGetActiveSubscriptions(...args),
    revokeExhaustedSubscription: (...args: unknown[]) => mockRevokeExhaustedSubscription(...args),
  },
}))

// createTestI18n supplies the subscription and confirmation labels used by this page
// fixture. Composition-mode translation renders those messages and their placeholders,
// allowing assertions to verify the visible confirmation text rather than raw keys.
function createTestI18n() {
  return createI18n({
    legacy: false,
    locale: 'en',
    messages: {
      en: {
        payment: {
          renewNow: 'Renew now',
        },
        common: {
          today: 'Today',
          tomorrow: 'Tomorrow',
          cancel: 'Cancel',
          confirm: 'Confirm',
          processing: 'Processing...',
        },
        userSubscriptions: {
          noActiveSubscriptions: 'No active subscriptions',
          queuedPacks: 'Queued {count}',
          useOrder: 'Use order {order}',
          moveUp: 'Use earlier',
          moveDown: 'Use later',
          buyAnother: 'Buy another',
          orderDescription: 'Subscriptions fund requests in the order shown.',
          orderSaved: 'Subscription order saved',
          orderFailed: 'Failed to save order',
          startsAt: 'Starts At',
          expires: 'Expires',
          currentPackEnds: 'Current pack ends {date}',
          daily: 'Daily',
          weekly: 'Weekly',
          monthly: 'Monthly',
          resetIn: 'Reset in {time}',
          quotaEndsIn: 'Ends in {time}',
          unlimited: 'Unlimited',
          unlimitedDesc: 'Unlimited usage',
          pendingOnly: 'Pending only',
          failedToLoad: 'Failed to load subscriptions',
          daysRemaining: '{days} days remaining',
          windowNotActive: 'Window not active',
          revoke: 'Revoke plan',
          revokeTitle: 'Revoke exhausted plan',
          revokeConfirmWithReplacement: 'Queued pack will start immediately.',
          revokeConfirmWithoutReplacement: 'No queued pack will replace this plan.',
          revokeSuccessWithReplacement: 'Plan revoked and {count} API key(s) rebound.',
          revokeSuccess: 'Plan revoked successfully.',
          revokeFailed: 'Failed to revoke plan.',
          groupAccess: 'Accessible groups',
          allGroups: 'All groups',
          restrictedGroups: 'Restricted groups',
          status: {
            active: 'Active',
            pending: 'Pending',
            expired: 'Expired',
          },
        },
      },
    },
  })
}

// mountView creates isolated stores, locale, and an initialized memory router before
// mounting the production subscription page. The confirmation stub preserves visibility,
// busy state, and emitted confirm or cancel actions.
async function mountView() {
  const pinia = createPinia()
  setActivePinia(pinia)
  const i18n = createTestI18n()
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/', component: { template: '<div />' } }],
  })
  await router.push('/')
  await router.isReady()

  return mount(SubscriptionsView, {
    global: {
      plugins: [pinia, i18n, router],
      stubs: {
        AppLayout: { template: '<div><slot /></div>' },
        Icon: { template: '<span />' },
        ConfirmDialog: {
          props: ['show', 'title', 'message', 'confirmText', 'cancelText', 'danger', 'loading'],
          emits: ['confirm', 'cancel'],
          template: `
            <div v-if="show" data-testid="revoke-dialog">
              <p data-testid="revoke-dialog-message">{{ message }}</p>
              <button data-testid="revoke-dialog-confirm" :disabled="loading" @click="$emit('confirm')">{{ confirmText }}</button>
              <button data-testid="revoke-dialog-cancel" :disabled="loading" @click="$emit('cancel')">{{ cancelText }}</button>
            </div>
          `,
        },
      },
    },
  })
}

describe('SubscriptionsView', () => {
  beforeEach(() => {
    mockGetMySubscriptions.mockReset()
    mockGetActiveSubscriptions.mockReset().mockResolvedValue([])
    mockRevokeExhaustedSubscription.mockReset()
    mockReorderSubscriptions.mockReset().mockResolvedValue(undefined)
    mockGetResetCards.mockReset().mockResolvedValue([])
    mockUseResetCard.mockReset()
  })

  // Active and pending records share one plan while another plan is also active.
  // Every purchase must retain its own card and identity in the spending order.
  it('shows every same-plan purchase as an independent card', async () => {
    mockGetMySubscriptions.mockResolvedValue([
      {
        id: 1,
        user_id: 7,
        plan_id: 101,
        starts_at: '2026-04-01T00:00:00Z',
        expires_at: '2026-05-01T00:00:00Z',
        status: 'active',
        daily_limit_usd: 10,
        weekly_limit_usd: null,
        monthly_limit_usd: null,
        daily_usage_usd: 3,
        weekly_usage_usd: 0,
        monthly_usage_usd: 0,
        daily_window_start: '2026-04-20T00:00:00Z',
        weekly_window_start: null,
        monthly_window_start: null,
        created_at: '2026-04-01T00:00:00Z',
        updated_at: '2026-04-01T00:00:00Z',
        plan: {
          id: 101,
          name: 'Plan Alpha',
          description: 'Alpha description',
          price: 10,
          features: [],
          validity_days: 30,
          validity_unit: 'day',
          daily_limit_usd: 10,
          weekly_limit_usd: null,
          monthly_limit_usd: null,
          for_sale: true,
          sort_order: 0,
        },
      },
      {
        id: 2,
        user_id: 7,
        plan_id: 101,
        starts_at: '2026-05-01T00:00:00Z',
        expires_at: '2026-05-31T00:00:00Z',
        status: 'pending',
        daily_limit_usd: 10,
        weekly_limit_usd: null,
        monthly_limit_usd: null,
        daily_usage_usd: 0,
        weekly_usage_usd: 0,
        monthly_usage_usd: 0,
        daily_window_start: null,
        weekly_window_start: null,
        monthly_window_start: null,
        created_at: '2026-04-10T00:00:00Z',
        updated_at: '2026-04-10T00:00:00Z',
        plan: {
          id: 101,
          name: 'Plan Alpha',
          description: 'Alpha description',
          price: 10,
          features: [],
          validity_days: 30,
          validity_unit: 'day',
          daily_limit_usd: 10,
          weekly_limit_usd: null,
          monthly_limit_usd: null,
          for_sale: true,
          sort_order: 0,
        },
      },
      {
        id: 3,
        user_id: 7,
        plan_id: 202,
        starts_at: '2026-04-15T00:00:00Z',
        expires_at: '2026-04-25T00:00:00Z',
        status: 'active',
        daily_limit_usd: null,
        weekly_limit_usd: 40,
        monthly_limit_usd: null,
        daily_usage_usd: 0,
        weekly_usage_usd: 8,
        monthly_usage_usd: 0,
        daily_window_start: null,
        weekly_window_start: '2026-04-18T00:00:00Z',
        monthly_window_start: null,
        created_at: '2026-04-15T00:00:00Z',
        updated_at: '2026-04-15T00:00:00Z',
        plan: {
          id: 202,
          name: 'Plan Beta',
          description: 'Beta description',
          price: 20,
          features: [],
          validity_days: 10,
          validity_unit: 'day',
          daily_limit_usd: null,
          weekly_limit_usd: 40,
          monthly_limit_usd: null,
          for_sale: true,
          sort_order: 0,
        },
      },
    ])

    const wrapper = await mountView()
    await flushPromises()

    const text = wrapper.text()
    expect(mockGetMySubscriptions).toHaveBeenCalledTimes(1)
    expect(text.match(/Plan Alpha/g)?.length).toBe(2)
    expect(text.match(/Plan Beta/g)?.length).toBe(1)
    expect(wrapper.findAll('article')).toHaveLength(3)
    expect(text).toContain('Use order 3')
    await wrapper.findAll('[data-testid="subscription-move-up"]')[1].trigger('click')
    await flushPromises()
    expect(mockReorderSubscriptions).toHaveBeenCalledWith([2, 1, 3])
    expect(wrapper.findAll('article')[0].text()).toContain('#2')
  })

  // Two active subscriptions share a plan but the card belongs to one purchase.
  // Confirmation must retain its bound identity even when the other subscription
  // appears first, and refresh card history after the server accepts redemption.
  it('uses a purchase reset card only on its bound subscription', async () => {
    mockGetMySubscriptions.mockResolvedValue([
      {
        id: 13,
        plan_id: 101,
        status: 'active',
        starts_at: '2026-11-01T00:00:00Z',
        expires_at: '2026-12-01T00:00:00Z',
        weekly_usage_usd: 100,
        weekly_limit_usd: 100,
        plan: { name: 'Light' },
      },
      {
        id: 11,
        plan_id: 101,
        status: 'active',
        starts_at: '2026-11-01T00:00:00Z',
        expires_at: '2026-12-01T00:00:00Z',
        weekly_usage_usd: 80,
        weekly_limit_usd: 100,
        plan: { name: 'Light' },
      },
      {
        id: 12,
        plan_id: 202,
        status: 'active',
        starts_at: '2026-11-01T00:00:00Z',
        expires_at: '2026-12-01T00:00:00Z',
        plan: { name: 'Premium' },
      },
    ])
    const card = {
      id: 7,
      plan_id: 101,
      source_subscription_id: 11,
      plan_name: 'Light',
      limit_usd: 585,
      expires_at: '2026-12-01T00:00:00Z',
      credited_usd: 0,
      status: 'available',
    }
    mockGetResetCards.mockResolvedValue([card])
    mockUseResetCard.mockImplementation(async () => {
      const used = {
        ...card,
        status: 'used',
        used_at: '2026-11-25T00:00:00Z',
        target_subscription_id: 11,
        credited_usd: 80,
      }
      mockGetResetCards.mockResolvedValue([used])
      return used
    })
    const wrapper = await mountView()
    await flushPromises()
    await wrapper.get('[data-testid="use-reset-card"]').trigger('click')
    await wrapper.get('[data-testid="revoke-dialog-confirm"]').trigger('click')
    await flushPromises()
    expect(mockUseResetCard).toHaveBeenCalledExactlyOnceWith(7, 11)
    expect(wrapper.find('[data-testid="use-reset-card"]').exists()).toBe(false)
    expect(mockGetMySubscriptions).toHaveBeenCalledTimes(2)
  })

  // Lower quota windows are exhausted while the highest configured window still has
  // capacity. The test protects revocation visibility only after that highest window also
  // reaches its limit.
  it('shows revoke only when the highest configured quota is exhausted', async () => {
    const now = Date.now()
    const subscription = {
      id: 9,
      user_id: 7,
      plan_id: 101,
      starts_at: new Date(now - 60 * 60 * 1000).toISOString(),
      expires_at: new Date(now + 24 * 60 * 60 * 1000).toISOString(),
      status: 'active',
      daily_limit_usd: 1,
      daily_usage_usd: 1,
      weekly_limit_usd: 10,
      weekly_usage_usd: 10,
      monthly_limit_usd: 100,
      monthly_usage_usd: 99,
      daily_window_start: new Date(now - 30 * 60 * 1000).toISOString(),
      weekly_window_start: new Date(now - 30 * 60 * 1000).toISOString(),
      monthly_window_start: new Date(now - 30 * 60 * 1000).toISOString(),
      created_at: new Date(now - 60 * 60 * 1000).toISOString(),
      updated_at: new Date(now - 60 * 60 * 1000).toISOString(),
      plan: {
        id: 101,
        name: 'Plan Alpha',
        description: '',
        price: 10,
        features: [],
        validity_days: 30,
        validity_unit: 'day',
        daily_limit_usd: 1,
        weekly_limit_usd: 10,
        monthly_limit_usd: 100,
        for_sale: true,
        sort_order: 0,
      },
    }
    mockGetMySubscriptions.mockResolvedValue([subscription])

    const wrapper = await mountView()
    await flushPromises()
    expect(wrapper.find('[data-testid="revoke-subscription"]').exists()).toBe(false)

    await wrapper.unmount()
    mockGetMySubscriptions.mockResolvedValue([{ ...subscription, monthly_usage_usd: 100 }])
    const exhaustedWrapper = await mountView()
    await flushPromises()
    expect(exhaustedWrapper.find('[data-testid="revoke-subscription"]').exists()).toBe(true)
  })

  // A revocation request remains pending after user confirmation. The test protects the
  // selected subscription identity, busy confirmation controls, and omission of duplicate
  // mutation requests.
  it('requires confirmation and prevents duplicate revoke submissions', async () => {
    const now = Date.now()
    mockGetMySubscriptions.mockResolvedValue([
      {
        id: 12,
        user_id: 7,
        plan_id: 303,
        starts_at: new Date(now - 60 * 60 * 1000).toISOString(),
        expires_at: new Date(now + 24 * 60 * 60 * 1000).toISOString(),
        status: 'active',
        daily_limit_usd: null,
        daily_usage_usd: 0,
        weekly_limit_usd: null,
        weekly_usage_usd: 0,
        monthly_limit_usd: 50,
        monthly_usage_usd: 50,
        daily_window_start: null,
        weekly_window_start: null,
        monthly_window_start: new Date(now - 30 * 60 * 1000).toISOString(),
        created_at: new Date(now - 60 * 60 * 1000).toISOString(),
        updated_at: new Date(now - 60 * 60 * 1000).toISOString(),
        plan: {
          id: 303,
          name: 'Plan Gamma',
          description: '',
          price: 10,
          features: [],
          validity_days: 30,
          validity_unit: 'day',
          daily_limit_usd: null,
          weekly_limit_usd: null,
          monthly_limit_usd: 50,
          for_sale: true,
          sort_order: 0,
        },
      },
    ])
    let resolveRevoke!: (value: {
      revoked_subscription_id: number /* Revoked subscription record identity. */
      replacement_subscription_id:
        | number
        | null /* Replacement record identity; null indicates that the server activated no queued subscription after revocation. */
      rebound_api_key_count: number /* Number of rebound API keys. */
    }) => void
    mockRevokeExhaustedSubscription.mockReturnValue(
      new Promise((resolve) => {
        resolveRevoke = resolve
      }),
    )

    const wrapper = await mountView()
    await flushPromises()
    await wrapper.get('[data-testid="revoke-subscription"]').trigger('click')
    const dialog = wrapper.get('[data-testid="revoke-dialog"]')
    expect(dialog.text()).toContain('No queued pack will replace this plan.')

    await wrapper.get('[data-testid="revoke-dialog-confirm"]').trigger('click')
    await wrapper.vm.$nextTick()
    expect(mockRevokeExhaustedSubscription).toHaveBeenCalledTimes(1)
    expect(mockRevokeExhaustedSubscription).toHaveBeenCalledWith(12)
    expect(
      wrapper.get('[data-testid="revoke-dialog-confirm"]').attributes('disabled'),
    ).toBeDefined()

    await wrapper.get('[data-testid="revoke-dialog-confirm"]').trigger('click')
    expect(mockRevokeExhaustedSubscription).toHaveBeenCalledTimes(1)
    resolveRevoke({
      revoked_subscription_id: 12,
      replacement_subscription_id: null,
      rebound_api_key_count: 0,
    })
    await flushPromises()
  })
})
