ALTER TABLE tutor_output_audits
    ADD COLUMN violations_json jsonb NULL
    CHECK (violations_json IS NULL OR jsonb_typeof(violations_json) = 'array');
