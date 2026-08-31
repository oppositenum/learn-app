CREATE TABLE content_sources (
    id uuid PRIMARY KEY,
    name text NOT NULL,
    source_type text NOT NULL CHECK (source_type IN ('CURRICULUM_STANDARD', 'TEXTBOOK_MAPPING', 'OPEN_LICENSE', 'INTERNAL_RULE', 'FACT_SOURCE')),
    license_code text NOT NULL,
    source_uri text,
    attribution text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE content_versions (
    id uuid PRIMARY KEY,
    question_id uuid NOT NULL REFERENCES questions(id) ON DELETE CASCADE,
    version text NOT NULL,
    schema_version text NOT NULL,
    generator_provider text NOT NULL,
    generator_model text NOT NULL,
    generator_request_id text,
    source_id uuid NOT NULL REFERENCES content_sources(id),
    asset_json jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (question_id, version)
);

CREATE TABLE content_validations (
    id uuid PRIMARY KEY,
    question_id uuid NOT NULL REFERENCES questions(id) ON DELETE CASCADE,
    content_version text NOT NULL,
    schema_version text NOT NULL,
    status text NOT NULL CHECK (status IN ('PASS', 'REJECTED_AUTOMATIC')),
    checks_json jsonb NOT NULL,
    validator_version text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE content_reviews (
    id uuid PRIMARY KEY,
    question_id uuid NOT NULL REFERENCES questions(id) ON DELETE CASCADE,
    content_version text NOT NULL,
    schema_version text NOT NULL,
    result text NOT NULL CHECK (result IN ('PASS', 'PASS_WITH_FIX', 'REJECT', 'NEEDS_HUMAN')),
    reviewer_provider text NOT NULL,
    reviewer_model text NOT NULL,
    reviewer_request_id text,
    findings_json jsonb NOT NULL DEFAULT '[]'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE content_release_records (
    id uuid PRIMARY KEY,
    question_id uuid NOT NULL REFERENCES questions(id) ON DELETE CASCADE,
    from_status text NOT NULL,
    to_status text NOT NULL CHECK (to_status IN ('RELEASED', 'QUARANTINED')),
    validation_id uuid REFERENCES content_validations(id),
    review_id uuid REFERENCES content_reviews(id),
    reason text NOT NULL,
    actor_user_id uuid REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX content_release_history ON content_release_records (question_id, created_at DESC);

CREATE FUNCTION enforce_question_content_state() RETURNS trigger AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        IF NEW.status <> 'DRAFT' THEN
            RAISE EXCEPTION 'new content must enter as DRAFT';
        END IF;
        RETURN NEW;
    END IF;
    IF NEW.status = OLD.status THEN
        RETURN NEW;
    END IF;
    IF NOT (
        (OLD.status = 'DRAFT' AND NEW.status IN ('AUTOMATIC_VALIDATED', 'REJECTED_AUTOMATIC')) OR
        (OLD.status = 'AUTOMATIC_VALIDATED' AND NEW.status = 'AI_REVIEWED') OR
        (OLD.status = 'AI_REVIEWED' AND NEW.status = 'RELEASED') OR
        (OLD.status = 'RELEASED' AND NEW.status = 'QUARANTINED') OR
        (OLD.status = 'QUARANTINED' AND NEW.status = 'DRAFT')
    ) THEN
        RAISE EXCEPTION 'invalid content transition: % -> %', OLD.status, NEW.status;
    END IF;
    IF NEW.status = 'AUTOMATIC_VALIDATED' AND NOT EXISTS (
        SELECT 1 FROM content_validations v
        WHERE v.question_id = NEW.id AND v.content_version = NEW.content_version AND v.status = 'PASS'
    ) THEN
        RAISE EXCEPTION 'automatic validation evidence missing';
    END IF;
    IF NEW.status = 'AI_REVIEWED' AND NOT EXISTS (
        SELECT 1 FROM content_reviews r
        WHERE r.question_id = NEW.id AND r.content_version = NEW.content_version AND r.result = 'PASS'
    ) THEN
        RAISE EXCEPTION 'independent review evidence missing';
    END IF;
    IF NEW.status = 'RELEASED' AND NOT EXISTS (
        SELECT 1 FROM content_validations v
        JOIN content_reviews r ON r.question_id = v.question_id
          AND r.content_version = v.content_version AND r.schema_version = v.schema_version
        JOIN content_versions cv ON cv.question_id = v.question_id
          AND cv.version = v.content_version AND cv.schema_version = v.schema_version
        WHERE v.question_id = NEW.id AND v.content_version = NEW.content_version
          AND v.status = 'PASS' AND r.result = 'PASS'
    ) THEN
        RAISE EXCEPTION 'release gate evidence missing or schema versions differ';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER questions_content_state_gate
BEFORE INSERT OR UPDATE OF status ON questions
FOR EACH ROW EXECUTE FUNCTION enforce_question_content_state();
