-- 315_subscription_reset_cards.sql snapshots purchase reset-card terms and
-- stores one-time weekly benefits separately from original subscription usage.
ALTER TABLE subscription_plans
    ADD COLUMN IF NOT EXISTS reset_card_count integer NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS reset_card_limit_usd numeric(20,8) NOT NULL DEFAULT 0,
    ADD CONSTRAINT subscription_plans_reset_cards_valid CHECK (
        reset_card_count BETWEEN 0 AND 100
        AND reset_card_limit_usd >= 0 AND reset_card_limit_usd < 1000000000000
        AND (reset_card_count = 0 OR (reset_card_limit_usd > 0 AND weekly_limit_usd IS NOT NULL AND weekly_limit_usd > 0))
    );

CREATE TABLE subscription_reset_cards (
    id bigserial PRIMARY KEY,
    user_id bigint NOT NULL REFERENCES users(id),
    plan_id bigint NOT NULL REFERENCES subscription_plans(id),
    source_subscription_id bigint NOT NULL REFERENCES user_subscriptions(id),
    ordinal integer NOT NULL CHECK (ordinal > 0),
    limit_usd numeric(20,8) NOT NULL CHECK (limit_usd > 0),
    expires_at timestamptz NOT NULL,
    target_subscription_id bigint REFERENCES user_subscriptions(id),
    used_at timestamptz,
    credited_usd numeric(20,8) NOT NULL DEFAULT 0 CHECK (credited_usd >= 0 AND credited_usd <= limit_usd),
    used_usd numeric(20,10) NOT NULL DEFAULT 0 CHECK (used_usd >= 0 AND used_usd <= credited_usd),
    benefit_expires_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT NOW(),
    CHECK (target_subscription_id IS NULL OR target_subscription_id = source_subscription_id),
    UNIQUE (source_subscription_id, ordinal)
);

CREATE INDEX subscription_reset_cards_owner ON subscription_reset_cards (user_id, id);
CREATE INDEX subscription_reset_cards_target ON subscription_reset_cards (target_subscription_id, benefit_expires_at)
    WHERE used_at IS NOT NULL;
