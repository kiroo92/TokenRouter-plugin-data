-- 316_subscription_reset_card_plan_policy.sql derives future purchase caps
-- from weekly quotas and requires both weekly and monthly limits. Previously
-- captured order terms and issued cards remain unchanged during this upgrade.
ALTER TABLE subscription_plans
    DROP CONSTRAINT IF EXISTS subscription_plans_reset_cards_valid;

-- Disable only previously enabled plans that lack eligible quotas. Never
-- enable an existing plan or rewrite its order snapshots and issued benefits.
UPDATE subscription_plans
SET reset_card_count = CASE
        WHEN weekly_limit_usd >= 0.00000001 AND weekly_limit_usd < 1000000000000
            AND monthly_limit_usd > 0 AND monthly_limit_usd < 'Infinity'::numeric
        THEN reset_card_count ELSE 0 END,
    reset_card_limit_usd = CASE
        WHEN reset_card_count > 0
            AND weekly_limit_usd >= 0.00000001 AND weekly_limit_usd < 1000000000000
            AND monthly_limit_usd > 0 AND monthly_limit_usd < 'Infinity'::numeric
        THEN weekly_limit_usd ELSE 0 END;

ALTER TABLE subscription_plans
    ADD CONSTRAINT subscription_plans_reset_cards_valid CHECK (
        reset_card_count BETWEEN 0 AND 100
        AND ((reset_card_count = 0 AND reset_card_limit_usd = 0)
            OR (reset_card_count > 0
                AND weekly_limit_usd IS NOT NULL
                AND weekly_limit_usd >= 0.00000001 AND weekly_limit_usd < 1000000000000
                AND monthly_limit_usd IS NOT NULL
                AND monthly_limit_usd > 0 AND monthly_limit_usd < 'Infinity'::numeric
                AND reset_card_limit_usd = weekly_limit_usd))
    );
