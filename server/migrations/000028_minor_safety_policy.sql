CREATE TABLE minor_safety_incidents (
    id uuid PRIMARY KEY,
    student_id uuid NOT NULL REFERENCES students(id),
    session_id uuid NOT NULL REFERENCES learning_sessions(id) ON DELETE CASCADE,
    policy_version text NOT NULL CHECK (length(trim(policy_version)) > 0),
    category text NOT NULL CHECK (category IN (
        'OFF_TOPIC_LONG',
        'PERSONAL_INFORMATION',
        'FAMILY_PRIVACY',
        'DANGEROUS_EXPERIMENT',
        'HEALTH',
        'SELF_HARM',
        'BULLYING',
        'SEXUAL_CONTENT'
    )),
    severity text NOT NULL CHECK (severity IN ('LOW', 'MODERATE', 'HIGH', 'CRITICAL')),
    fixed_action text NOT NULL CHECK (fixed_action IN (
        'RETURN_TO_LEARNING',
        'PROTECT_PRIVACY',
        'TALK_TO_TRUSTED_ADULT',
        'STOP_EXPERIMENT_AND_GET_ADULT',
        'SEEK_HEALTH_HELP',
        'SEEK_URGENT_HELP',
        'REPORT_BULLYING',
        'PROTECT_BODY_AND_GET_ADULT'
    )),
    classification_source text NOT NULL DEFAULT 'DETERMINISTIC' CHECK (classification_source = 'DETERMINISTIC'),
    parent_escalated boolean NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX minor_safety_incidents_student_created
    ON minor_safety_incidents(student_id, created_at DESC);

CREATE TABLE minor_safety_access_audits (
    id uuid PRIMARY KEY,
    incident_id uuid NOT NULL REFERENCES minor_safety_incidents(id) ON DELETE RESTRICT,
    accessor_user_id uuid NOT NULL REFERENCES users(id),
    channel text NOT NULL CHECK (channel IN ('PARENT_SUMMARY_API')),
    action text NOT NULL DEFAULT 'VIEWED' CHECK (action = 'VIEWED'),
    accessed_at timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE minor_safety_incidents IS
    'Versioned minimal safety classifications only. Student input and model prose are intentionally absent.';

COMMENT ON TABLE minor_safety_access_audits IS
    'Audits authorized access to minimal parent safety summaries; no student input is stored.';
