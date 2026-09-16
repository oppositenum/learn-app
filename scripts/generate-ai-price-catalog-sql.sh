#!/bin/sh
set -eu

usage() {
  cat >&2 <<'USAGE'
usage: generate-ai-price-catalog-sql.sh \
  --id UUID --provider NAME --model NAME --effective-from RFC3339 \
  --fx-cny-per-usd DECIMAL --fx-source TEXT --fx-date YYYY-MM-DD \
  --input-cny-per-million DECIMAL --cached-input-cny-per-million DECIMAL \
  --output-cny-per-million DECIMAL --audio-input-cny-per-minute DECIMAL \
  --audio-output-cny-per-minute DECIMAL --cost-tier LOW|STANDARD|STRONG

The script prints SQL only. It does not connect to PostgreSQL. All CNY prices
are divided by the supplied CNY-per-USD rate and rounded to nine USD decimals.
USAGE
  exit 2
}

catalog_id=
provider=
model=
effective_from=
fx_rate=
fx_source=
fx_date=
input_cny=
cached_input_cny=
output_cny=
audio_input_cny=
audio_output_cny=
cost_tier=

while [ "$#" -gt 0 ]; do
  [ "$#" -ge 2 ] || usage
  case "$1" in
    --id) catalog_id=$2 ;;
    --provider) provider=$2 ;;
    --model) model=$2 ;;
    --effective-from) effective_from=$2 ;;
    --fx-cny-per-usd) fx_rate=$2 ;;
    --fx-source) fx_source=$2 ;;
    --fx-date) fx_date=$2 ;;
    --input-cny-per-million) input_cny=$2 ;;
    --cached-input-cny-per-million) cached_input_cny=$2 ;;
    --output-cny-per-million) output_cny=$2 ;;
    --audio-input-cny-per-minute) audio_input_cny=$2 ;;
    --audio-output-cny-per-minute) audio_output_cny=$2 ;;
    --cost-tier) cost_tier=$2 ;;
    *) usage ;;
  esac
  shift 2
done

is_decimal() {
  printf '%s\n' "$1" | awk '/^(0|[1-9][0-9]*)(\.[0-9]+)?$/ { ok=1 } END { exit !ok }'
}

for value in "$fx_rate" "$input_cny" "$cached_input_cny" "$output_cny" "$audio_input_cny" "$audio_output_cny"; do
  [ -n "$value" ] && is_decimal "$value" || usage
done
awk -v rate="$fx_rate" 'BEGIN { exit !(rate > 0) }' || usage

printf '%s\n' "$catalog_id" | awk '/^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-5][0-9a-fA-F]{3}-[89aAbB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$/ { ok=1 } END { exit !ok }' || usage
awk -v value="$provider" 'BEGIN { exit !(value ~ "^[A-Za-z0-9._-]+$") }' || usage
awk -v value="$model" 'BEGIN { exit !(value ~ "^[A-Za-z0-9._:/-]+$") }' || usage
printf '%s\n' "$effective_from" | awk '/^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]+)?(Z|[+-][0-9]{2}:[0-9]{2})$/ { ok=1 } END { exit !ok }' || usage
printf '%s\n' "$fx_date" | awk '/^[0-9]{4}-[0-9]{2}-[0-9]{2}$/ { ok=1 } END { exit !ok }' || usage
awk -v value="$fx_source" 'BEGIN { exit !(value ~ "^[A-Za-z0-9 ._:/()+-]+$") }' || usage
case "$cost_tier" in LOW|STANDARD|STRONG) ;; *) usage ;; esac

to_usd() {
  awk -v cny="$1" -v rate="$fx_rate" 'BEGIN { printf "%.9f", cny / rate }'
}

input_usd=$(to_usd "$input_cny")
cached_input_usd=$(to_usd "$cached_input_cny")
output_usd=$(to_usd "$output_cny")
audio_input_usd=$(to_usd "$audio_input_cny")
audio_output_usd=$(to_usd "$audio_output_cny")

cat <<SQL
-- Controlled CNY-to-USD conversion for ai_price_catalog.
-- FX source: $fx_source
-- FX observation date: $fx_date
-- FX rate (CNY per USD): $fx_rate
-- Source prices (CNY): input/million=$input_cny cached-input/million=$cached_input_cny output/million=$output_cny audio-input/minute=$audio_input_cny audio-output/minute=$audio_output_cny
-- Context cache write is not represented by the current schema and must remain disabled.
BEGIN;
UPDATE ai_price_catalog
SET effective_to = '$effective_from'::timestamptz
WHERE provider = '$provider'
  AND model = '$model'
  AND effective_to IS NULL
  AND effective_from < '$effective_from'::timestamptz;
INSERT INTO ai_price_catalog (
  id, provider, model, effective_from, effective_to,
  input_price_per_million_usd, cached_input_price_per_million_usd,
  output_price_per_million_usd, audio_input_price_per_minute_usd,
  audio_output_price_per_minute_usd, cost_tier
) VALUES (
  '$catalog_id'::uuid, '$provider', '$model', '$effective_from'::timestamptz, NULL,
  $input_usd, $cached_input_usd, $output_usd, $audio_input_usd, $audio_output_usd, '$cost_tier'
);
COMMIT;
SQL
