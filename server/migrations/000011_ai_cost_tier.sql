ALTER TABLE ai_price_catalog
ADD COLUMN cost_tier text NOT NULL DEFAULT 'STANDARD'
CHECK (cost_tier IN ('LOW','STANDARD','STRONG'));
