CREATE TABLE classroom_task_lineages (
    id uuid PRIMARY KEY,
    knowledge_point_id uuid NOT NULL REFERENCES knowledge_points(id),
    version text NOT NULL CHECK (length(trim(version)) > 0),
    status text NOT NULL CHECK (status IN ('READY', 'RETIRED')),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (knowledge_point_id, version)
);

CREATE TABLE classroom_stage_tasks (
    question_id uuid PRIMARY KEY REFERENCES questions(id),
    lineage_id uuid NOT NULL REFERENCES classroom_task_lineages(id),
    stage_role text NOT NULL CHECK (stage_role IN ('ORIGINAL', 'VARIANT', 'ABSTRACT', 'VERIFY')),
    selection_order smallint NOT NULL CHECK (selection_order > 0),
    scoring_rule_version text NOT NULL CHECK (length(trim(scoring_rule_version)) > 0),
    scoring_rule_private_json jsonb NOT NULL,
    evidence_form text NOT NULL CHECK (evidence_form IN ('LIFE', 'VARIANT', 'TEXTBOOK', 'REVIEW')),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (lineage_id, stage_role, selection_order),
    UNIQUE (lineage_id, question_id)
);

REVOKE ALL ON classroom_stage_tasks FROM PUBLIC;

CREATE TABLE classroom_stage_sessions (
    session_id uuid PRIMARY KEY REFERENCES learning_sessions(id) ON DELETE CASCADE,
    lineage_id uuid NOT NULL REFERENCES classroom_task_lineages(id),
    knowledge_point_id uuid NOT NULL REFERENCES knowledge_points(id),
    current_task_id uuid NOT NULL REFERENCES classroom_stage_tasks(question_id),
    current_task_version text NOT NULL,
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    started_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz
);

CREATE TABLE classroom_stage_attempts (
    id uuid PRIMARY KEY,
    session_id uuid NOT NULL REFERENCES classroom_stage_sessions(session_id) ON DELETE CASCADE,
    operation_id uuid NOT NULL,
    request_digest text NOT NULL CHECK (request_digest ~ '^[0-9a-f]{64}$'),
    submitted_stage text NOT NULL CHECK (submitted_stage IN ('ORIGINAL', 'VARIANT', 'ABSTRACT', 'VERIFY')),
    question_id uuid NOT NULL REFERENCES classroom_stage_tasks(question_id),
    question_version text NOT NULL,
    attempt_kind text NOT NULL CHECK (attempt_kind IN ('ANSWER', 'HELP')),
    support_type text CHECK (support_type IS NULL OR support_type IN ('HINT', 'EXPLAIN')),
    deterministic_result text NOT NULL CHECK (deterministic_result IN ('CORRECT', 'INCORRECT', 'INDETERMINATE', 'HELP_REQUESTED')),
    feedback_delivered boolean NOT NULL DEFAULT false,
    task_success boolean NOT NULL DEFAULT false,
    evidence_kind text NOT NULL CHECK (evidence_kind IN ('NONE', 'ASSISTED', 'INDEPENDENT')),
    stage_completed boolean NOT NULL DEFAULT false,
    response_code text NOT NULL CHECK (response_code IN ('TRY_NEW_TASK', 'HELP_DELIVERED', 'ASSISTED_REPROOF', 'NEXT_STAGE', 'CLASSROOM_COMPLETE')),
    response_stage text NOT NULL CHECK (response_stage IN ('ORIGINAL', 'VARIANT', 'ABSTRACT', 'VERIFY', 'COMPLETE')),
    response_task_id uuid REFERENCES classroom_stage_tasks(question_id),
    response_task_version text,
    response_session_version bigint NOT NULL,
    response_timing_version bigint NOT NULL,
    response_status text NOT NULL CHECK (response_status IN ('ACTIVE', 'PAUSED', 'COMPLETED')),
    response_active_seconds integer NOT NULL CHECK (response_active_seconds >= 0),
    response_current_seconds integer NOT NULL CHECK (response_current_seconds >= 0),
    response_observed_at timestamptz NOT NULL,
    session_completed boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (session_id, operation_id),
    CHECK (task_success = (evidence_kind <> 'NONE')),
    CHECK ((attempt_kind = 'ANSWER' AND support_type IS NULL)
        OR (attempt_kind = 'HELP' AND support_type IS NOT NULL)),
    CHECK (NOT stage_completed OR (task_success AND evidence_kind = 'INDEPENDENT')),
    CHECK (feedback_delivered = (attempt_kind = 'HELP' OR deterministic_result IN ('INCORRECT', 'INDETERMINATE'))),
    CHECK ((response_stage = 'COMPLETE' AND response_task_id IS NULL AND response_task_version IS NULL AND session_completed)
        OR (response_stage <> 'COMPLETE' AND response_task_id IS NOT NULL AND response_task_version IS NOT NULL AND NOT session_completed))
);

CREATE UNIQUE INDEX classroom_stage_one_task_success
ON classroom_stage_attempts(session_id, submitted_stage, question_id)
WHERE task_success;

CREATE UNIQUE INDEX classroom_stage_one_stage_completion
ON classroom_stage_attempts(session_id, submitted_stage)
WHERE stage_completed;

CREATE TABLE classroom_stage_evidence (
    id uuid PRIMARY KEY,
    session_id uuid NOT NULL REFERENCES classroom_stage_sessions(session_id) ON DELETE RESTRICT,
    attempt_id uuid NOT NULL UNIQUE REFERENCES classroom_stage_attempts(id) ON DELETE RESTRICT,
    student_id uuid NOT NULL REFERENCES students(id),
    knowledge_point_id uuid NOT NULL REFERENCES knowledge_points(id),
    stage_role text NOT NULL CHECK (stage_role IN ('ORIGINAL', 'VARIANT', 'ABSTRACT', 'VERIFY')),
    evidence_kind text NOT NULL CHECK (evidence_kind IN ('ASSISTED', 'INDEPENDENT')),
    evidence_form text NOT NULL CHECK (evidence_form IN ('LIFE', 'VARIANT', 'TEXTBOOK', 'REVIEW')),
    authorized_for_mastery boolean NOT NULL DEFAULT false,
    authorization_policy_version text NOT NULL,
    scoring_rule_version text NOT NULL,
    question_version text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK (length(trim(authorization_policy_version)) > 0),
    CHECK (length(trim(scoring_rule_version)) > 0),
    CHECK (length(trim(question_version)) > 0),
    CHECK (authorized_for_mastery = (evidence_kind = 'INDEPENDENT'))
);

CREATE UNIQUE INDEX classroom_stage_one_task_evidence
ON classroom_stage_evidence(session_id, stage_role, attempt_id);

COMMENT ON TABLE classroom_stage_attempts IS
    'Minimal deterministic stage outcomes. Response bodies and model prose are absent; request_digest is a response-derived operation fingerprint retained for exact idempotency conflict detection.';

COMMENT ON TABLE classroom_stage_evidence IS
    'Task-derived evidence provenance. Only independent deterministic stage completions authorize mastery counters.';
