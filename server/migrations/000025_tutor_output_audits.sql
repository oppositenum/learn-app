CREATE TABLE tutor_output_audits (
    id uuid PRIMARY KEY,
    student_id uuid NOT NULL REFERENCES students(id),
    session_id uuid NOT NULL REFERENCES learning_sessions(id) ON DELETE CASCADE,
    generation_response_id text NOT NULL DEFAULT '',
    reviewer_provider text NOT NULL CHECK (reviewer_provider <> ''),
    reviewer_model text NOT NULL CHECK (reviewer_model <> ''),
    reviewer_request_id text NOT NULL CHECK (reviewer_request_id <> ''),
    policy_version text NOT NULL CHECK (policy_version <> ''),
    deterministic_result text NOT NULL CHECK (deterministic_result IN ('PASS', 'REJECT')),
    reviewer_result text NOT NULL CHECK (reviewer_result IN ('PASS', 'REJECT', 'INVALID_SCHEMA', 'INVALID_PROVENANCE', 'TIMEOUT', 'ERROR')),
    final_result text NOT NULL CHECK (final_result IN ('PASS', 'REJECT')),
    reason_code text NOT NULL CHECK (reason_code <> ''),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX tutor_output_audits_session_created
    ON tutor_output_audits (session_id, created_at DESC);
