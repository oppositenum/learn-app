ALTER TABLE parent_preferences
    ADD COLUMN enabled_subject_codes text[] NOT NULL DEFAULT '{}'
    CHECK (enabled_subject_codes <@ ARRAY['MATH','CHINESE','ENGLISH','PHYSICS','CHEMISTRY']::text[]);
