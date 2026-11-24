-- 314_subscription_consumption_order.sql adds persistent spending ranks and an
-- independent-lifetime marker to issued subscriptions. Existing grants retain
-- their saved dates and legacy chain behavior. New issuance sets independent
-- explicitly, while automatic funding uses the rank before expiry and ID.
ALTER TABLE user_subscriptions
    ADD COLUMN IF NOT EXISTS consumption_priority integer NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS independent boolean NOT NULL DEFAULT false;

CREATE INDEX IF NOT EXISTS idx_user_subscriptions_consumption_order
    ON user_subscriptions (user_id, consumption_priority, expires_at, starts_at, id)
    WHERE deleted_at IS NULL;
