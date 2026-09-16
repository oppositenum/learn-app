# Provider Price Registration In USD

## Decision

Doubao and Qwen catalog entries use the existing USD columns. No currency column or runtime exchange-rate conversion is added. Each source price is converted once using a controlled `CNY per USD` rate, then inserted as a new versioned `ai_price_catalog` row.

The exchange-rate value, source, observation date, effective timestamp, and original CNY prices are emitted as SQL comments by `scripts/generate-ai-price-catalog-sql.sh`. The generated SQL is the reviewable evidence; it must be retained with the deployment change that applies the price row. Business code contains no model price or exchange rate.

## Procedure

1. Obtain the vendor's current CNY prices from an authorized source.
2. Obtain an approved CNY-per-USD rate and record its source and observation date.
3. Choose a new UUID and an RFC3339 effective timestamp.
4. Run the generator with all token and audio price dimensions explicitly supplied. Use `0` only when the vendor contract states that the dimension is not charged or the capability is not used.
5. Review the comments and nine-decimal USD calculations in the output.
6. Apply the SQL through the controlled database process. The SQL closes the prior open version for the same provider/model before inserting the new row.
7. Verify that provider calls use exactly the configured `(provider, model)` pair and that resulting `ai_usage_records.price_catalog_id` points to the intended effective row.

Example with deliberately non-production values:

```sh
scripts/generate-ai-price-catalog-sql.sh \
  --id 11111111-1111-4111-8111-111111111111 \
  --provider example-provider \
  --model example-model \
  --effective-from 2026-09-16T00:00:00+08:00 \
  --fx-cny-per-usd 7.000000 \
  --fx-source 'Example approved source' \
  --fx-date 2026-09-16 \
  --input-cny-per-million 7 \
  --cached-input-cny-per-million 0 \
  --output-cny-per-million 14 \
  --audio-input-cny-per-minute 0 \
  --audio-output-cny-per-minute 0 \
  --cost-tier STANDARD
```

The example values are test inputs, not Doubao or Qwen prices and must not be applied as production catalog data.

## Context Cache Boundary

The current catalog has a cached-input read price but no distinct cache-write price. Tutor channel configuration therefore defaults provider-side context caching to disabled and rejects any attempt to enable it. No cache-write price may be placed in token-read or audio columns. Conversation linkage through `previous_response_id` is a separate context-continuity mechanism; it does not authorize a billable provider cache feature.
