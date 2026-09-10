CREATE TABLE classroom_stage_safety_operations (
    session_id uuid NOT NULL REFERENCES classroom_stage_sessions(session_id) ON DELETE CASCADE,
    operation_id uuid NOT NULL,
    incident_id uuid NOT NULL UNIQUE REFERENCES minor_safety_incidents(id) ON DELETE RESTRICT,
    attempt_kind text NOT NULL CHECK (attempt_kind = 'ANSWER'),
    submitted_stage text NOT NULL CHECK (submitted_stage IN ('ORIGINAL', 'VARIANT', 'ABSTRACT', 'VERIFY')),
    question_id uuid NOT NULL REFERENCES classroom_stage_tasks(question_id),
    question_version text NOT NULL CHECK (length(trim(question_version)) > 0),
    response_session_version bigint NOT NULL CHECK (response_session_version > 0),
    response_timing_version bigint NOT NULL CHECK (response_timing_version >= 0),
    response_socratic_round smallint NOT NULL CHECK (response_socratic_round BETWEEN 0 AND 3),
    response_status text NOT NULL CHECK (response_status IN ('ACTIVE', 'PAUSED')),
    response_active_seconds integer NOT NULL CHECK (response_active_seconds >= 0),
    response_current_seconds integer NOT NULL CHECK (response_current_seconds >= 0),
    response_observed_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (session_id, operation_id)
);

COMMENT ON TABLE classroom_stage_safety_operations IS
    'Privacy-preserving idempotency for structured safety interventions. Stores public task identity and bounded response metadata only; child response bodies, response-derived fingerprints, excerpts, and model prose are absent. Reuse with the same public identity is first-write-wins.';

COMMENT ON TABLE classroom_stage_attempts IS
    'Minimal deterministic stage outcomes. Response bodies and model prose are absent; request_digest is a response-derived operation fingerprint retained for exact idempotency conflict detection.';

REVOKE ALL ON classroom_stage_attempts, classroom_stage_evidence, classroom_stage_safety_operations FROM PUBLIC;

UPDATE classroom_stage_attempts
SET response_action=CASE
    WHEN attempt_kind='HELP' THEN support_type
    WHEN response_code='SOCRATIC_LIMIT_EXPLAINED' THEN 'EXPLAIN'
    WHEN response_code='CONTENT_EXHAUSTED' THEN response_stage
    WHEN deterministic_result IN ('INCORRECT','INDETERMINATE') THEN 'PROBE'
    ELSE response_stage
END
WHERE response_action IS DISTINCT FROM CASE
    WHEN attempt_kind='HELP' THEN support_type
    WHEN response_code='SOCRATIC_LIMIT_EXPLAINED' THEN 'EXPLAIN'
    WHEN response_code='CONTENT_EXHAUSTED' THEN response_stage
    WHEN deterministic_result IN ('INCORRECT','INDETERMINATE') THEN 'PROBE'
    ELSE response_stage
END;
