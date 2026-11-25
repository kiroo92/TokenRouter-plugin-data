package billing

import (
	"context"
	"math"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// subscription_reset_card.go defines paid-purchase reset cards and their weekly
// benefit balances. A card restores already settled spending up to its captured
// cap on the originating paid subscription. It leaves original usage,
// daily and monthly allowances, and every refresh timestamp unchanged.
//
// Used cards become separate funding sources until the target's original week
// ends or that subscription expires. Their identities follow task holds into
// capture and release, so refunds cannot reduce a subscription's original usage
// or restore an expired week's capacity. Storage owns payer-first locking and
// atomically records the card's one-time consumption with its credited amount.

// ResetCardError identifies unavailable cards or grants without exposing
// whether another user owns the supplied identifier.
var ResetCardError = apperror.BadRequest("SUBSCRIPTION_RESET_CARD_UNAVAILABLE", "reset card or subscription is unavailable")

// WeeklyResetCredit is the usable balance of one redeemed purchase card.
type WeeklyResetCredit struct {
	ID           int64     `json:"id"`            // consumed reset card identity
	RemainingUSD float64   `json:"remaining_usd"` // USD unreserved benefit balance
	ExpiresAt    time.Time `json:"expires_at"`    // exclusive original-week benefit expiry
}

// SubscriptionResetCard records a paid grant's captured one-time benefit.
type SubscriptionResetCard struct {
	ID                   int64      `json:"id"`                               // issued reset card identity
	PlanID               int64      `json:"plan_id"`                          // exact eligible subscription plan
	SourceSubscriptionID int64      `json:"source_subscription_id"`           // originating paid grant
	PlanName             string     `json:"plan_name"`                        // current plan display label
	LimitUSD             float64    `json:"limit_usd"`                        // USD captured per-card cap
	ExpiresAt            time.Time  `json:"expires_at"`                       // exclusive card expiry from purchase
	UsedAt               *time.Time `json:"used_at,omitempty"`                // successful redemption timestamp
	TargetSubscriptionID *int64     `json:"target_subscription_id,omitempty"` // redeemed originating paid grant
	CreditedUSD          float64    `json:"credited_usd"`                     // USD originally restored by redemption
	RemainingUSD         float64    `json:"remaining_usd"`                    // USD currently unreserved benefit
	BenefitExpiresAt     *time.Time `json:"benefit_expires_at,omitempty"`     // exclusive original-week expiry
	Status               string     `json:"status"`                           // available, used, expired, or unavailable
}

// SubscriptionResetCardStore supplies atomic issuance, redemption and benefit reads.
type SubscriptionResetCardStore interface {
	// IssueResetCards participates in the grant transaction and creates captured
	// purchase benefits once. The caller supplies a positive paid-order grant.
	IssueResetCards(context.Context, *UserSubscription, int, float64) error
	// ListResetCards returns only the owner's cards with effective expiry and
	// source availability; an expired source never authorizes a new redemption.
	ListResetCards(context.Context, int64) ([]SubscriptionResetCard, error)
	// UseResetCard locks the payer before card and bound-subscription eligibility.
	// Only the originating paid grant can receive this card's weekly benefit;
	// repeating the same card and grant returns the original successful result.
	UseResetCard(context.Context, int64, int64, int64) (*SubscriptionResetCard, error)
	// LoadResetCredits enriches the supplied grant views from current unexpired
	// benefit balances without changing original quota counters or their anchors.
	LoadResetCredits(context.Context, []UserSubscription) error
}

// ValidateResetCardPolicy checks the configured included-card quantity and cap.
// Disabled issuance accepts a nonnegative cap; enabled issuance also requires
// a finite positive weekly quota and a finite positive per-card credit cap.
func ValidateResetCardPolicy(count int, cap float64, weekly *float64) error {
	// One quota unit is 1e-8 USD. Smaller enabled caps round to zero in
	// persistence and cannot issue a usable purchase benefit.
	if count < 0 || count > 100 || math.IsNaN(cap) || math.IsInf(cap, 0) || cap < 0 || cap >= 1e12 ||
		(count > 0 && (cap < 0.00000001 || !FinitePositiveSubscriptionLimit(weekly))) {
		return apperror.BadRequest("INVALID_RESET_CARD_POLICY", "reset cards require a count from 1 to 100, a positive cap and a finite weekly quota")
	}
	return nil
}

// AvailableResetCredit sums unexpired benefit balances for the supplied time.
// Negative or fully reserved balances contribute nothing; callers still check
// grant ownership, suspension, lifetime and group eligibility before funding.
func AvailableResetCredit(credits []WeeklyResetCredit, now time.Time) float64 {
	amount := 0.0
	for _, credit := range credits {
		if credit.ExpiresAt.After(now) && credit.RemainingUSD > 0 {
			amount += credit.RemainingUSD
		}
	}
	return amount
}

// loadResetCredits attaches current benefit capacity when persistence supports
// purchase cards. In-memory fixtures lacking that capability retain their supplied
// snapshots; database failures propagate rather than silently charging balance.
func (s *SubscriptionService) loadResetCredits(ctx context.Context, subs []UserSubscription) error {
	if store, ok := s.userSubRepo.(SubscriptionResetCardStore); ok && len(subs) > 0 {
		return store.LoadResetCredits(ctx, subs)
	}
	return nil
}

// ListResetCards returns the authenticated payer's purchase-card inventory.
// Missing persistence reports the feature unavailable; callers derive the payer
// from authentication and never accept another account ID from request JSON.
func (s *SubscriptionService) ListResetCards(ctx context.Context, userID int64) ([]SubscriptionResetCard, error) {
	store, ok := s.userSubRepo.(SubscriptionResetCardStore)
	if !ok || userID <= 0 {
		return nil, ResetCardError
	}
	return store.ListResetCards(ctx, userID)
}

// UseResetCard redeems one owned card against its originating paid subscription.
// Storage serializes its ownership, status, original-week and settled-use checks
// with billing. Replays retain the original credit without issuing another benefit.
// @project-doc docs/domains/payments_and_entitlements.md#subscription_weekly_reset_cards
func (s *SubscriptionService) UseResetCard(ctx context.Context, userID, cardID, subscriptionID int64) (*SubscriptionResetCard, error) {
	store, ok := s.userSubRepo.(SubscriptionResetCardStore)
	if !ok || userID <= 0 || cardID <= 0 || subscriptionID <= 0 {
		return nil, ResetCardError
	}
	return store.UseResetCard(ctx, userID, cardID, subscriptionID)
}
