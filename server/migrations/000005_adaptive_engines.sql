CREATE TABLE parent_preferences (
    parent_user_id uuid NOT NULL REFERENCES users(id),
    student_id uuid NOT NULL REFERENCES students(id),
    daily_minutes smallint NOT NULL DEFAULT 30 CHECK (daily_minutes BETWEEN 15 AND 60),
    priority_subject_codes text[] NOT NULL DEFAULT '{}',
    review_only boolean NOT NULL DEFAULT false,
    reduce_intensity boolean NOT NULL DEFAULT false,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (parent_user_id, student_id)
);

CREATE TABLE student_skill_states (
    student_id uuid NOT NULL REFERENCES students(id),
    knowledge_point_id uuid NOT NULL REFERENCES knowledge_points(id),
    state text NOT NULL CHECK (state IN ('UNKNOWN', 'EXPOSED', 'LEARNING', 'ASSISTED', 'UNDERSTOOD', 'MASTERED', 'REVIEW_DUE', 'REGRESSED')),
    score_internal numeric(5,2) NOT NULL DEFAULT 0 CHECK (score_internal BETWEEN 0 AND 100),
    independent_successes integer NOT NULL DEFAULT 0,
    assisted_successes integer NOT NULL DEFAULT 0,
    life_context_successes integer NOT NULL DEFAULT 0,
    variant_successes integer NOT NULL DEFAULT 0,
    textbook_successes integer NOT NULL DEFAULT 0,
    review_successes integer NOT NULL DEFAULT 0,
    consecutive_review_failures integer NOT NULL DEFAULT 0,
    last_seen_at timestamptz,
    next_review_at timestamptz,
    version bigint NOT NULL DEFAULT 1,
    PRIMARY KEY (student_id, knowledge_point_id)
);

CREATE TABLE student_ability_states (
    student_id uuid NOT NULL REFERENCES students(id),
    core_ability_id uuid NOT NULL REFERENCES core_abilities(id),
    score_internal numeric(5,2) NOT NULL DEFAULT 0 CHECK (score_internal BETWEEN 0 AND 100),
    evidence_count integer NOT NULL DEFAULT 0,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (student_id, core_ability_id)
);

CREATE TABLE student_misconceptions (
    student_id uuid NOT NULL REFERENCES students(id),
    knowledge_point_id uuid NOT NULL REFERENCES knowledge_points(id),
    misconception_id uuid NOT NULL REFERENCES misconceptions(id),
    first_seen_at timestamptz NOT NULL,
    last_seen_at timestamptz NOT NULL,
    occurrences integer NOT NULL DEFAULT 1,
    successful_corrections integer NOT NULL DEFAULT 0,
    status text NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'MONITORING', 'RESOLVED')),
    PRIMARY KEY (student_id, knowledge_point_id, misconception_id)
);

CREATE TABLE review_queue (
    id uuid PRIMARY KEY,
    student_id uuid NOT NULL REFERENCES students(id),
    knowledge_point_id uuid NOT NULL REFERENCES knowledge_points(id),
    source text NOT NULL CHECK (source IN ('MASTERY', 'MISCONCEPTION', 'REGRESSION', 'PARENT_PRIORITY')),
    due_at timestamptz NOT NULL,
    priority smallint NOT NULL DEFAULT 50 CHECK (priority BETWEEN 1 AND 100),
    attempts integer NOT NULL DEFAULT 0,
    status text NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING', 'COMPLETED', 'CANCELLED')),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX review_queue_due ON review_queue (student_id, due_at, priority DESC) WHERE status = 'PENDING';

CREATE TABLE learning_plans (
    id uuid PRIMARY KEY,
    student_id uuid NOT NULL REFERENCES students(id),
    plan_date date NOT NULL,
    target_minutes smallint NOT NULL CHECK (target_minutes BETWEEN 1 AND 120),
    status text NOT NULL DEFAULT 'PROPOSED' CHECK (status IN ('PROPOSED', 'ACTIVE', 'COMPLETED', 'REPLACED')),
    based_on_session_id uuid REFERENCES learning_sessions(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (student_id, plan_date, status)
);

CREATE TABLE learning_plan_blocks (
    id uuid PRIMARY KEY,
    plan_id uuid NOT NULL REFERENCES learning_plans(id) ON DELETE CASCADE,
    sequence smallint NOT NULL,
    subject_id uuid NOT NULL REFERENCES subjects(id),
    knowledge_point_id uuid REFERENCES knowledge_points(id),
    minutes smallint NOT NULL CHECK (minutes BETWEEN 1 AND 60),
    mode text NOT NULL CHECK (mode IN ('REMEDIATION', 'CURRENT_GRADE', 'REVIEW', 'MICRO_BACKTRACK')),
    reason text NOT NULL,
    original_task_id uuid,
    UNIQUE (plan_id, sequence)
);

CREATE TABLE reward_events (
    id uuid PRIMARY KEY,
    student_id uuid NOT NULL REFERENCES students(id),
    session_id uuid REFERENCES learning_sessions(id),
    type text NOT NULL,
    points integer NOT NULL CHECK (points >= 0),
    source_id text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (student_id, type, source_id)
);

CREATE TABLE student_growth (
    student_id uuid PRIMARY KEY REFERENCES students(id),
    total_energy integer NOT NULL DEFAULT 0 CHECK (total_energy >= 0),
    streak_days integer NOT NULL DEFAULT 0 CHECK (streak_days >= 0),
    buildings_json jsonb NOT NULL DEFAULT '{}'::jsonb,
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE ai_price_catalog (
    id uuid PRIMARY KEY,
    provider text NOT NULL,
    model text NOT NULL,
    effective_from timestamptz NOT NULL,
    effective_to timestamptz,
    input_price_per_million_usd numeric(18,9) NOT NULL,
    cached_input_price_per_million_usd numeric(18,9) NOT NULL,
    output_price_per_million_usd numeric(18,9) NOT NULL,
    audio_input_price_per_minute_usd numeric(18,9) NOT NULL DEFAULT 0,
    audio_output_price_per_minute_usd numeric(18,9) NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (provider, model, effective_from),
    CONSTRAINT ai_price_effective_range CHECK (effective_to IS NULL OR effective_to > effective_from)
);

CREATE TABLE ai_usage_records (
    request_id text PRIMARY KEY,
    student_id uuid REFERENCES students(id),
    session_id uuid REFERENCES learning_sessions(id),
    provider text NOT NULL,
    model text NOT NULL,
    purpose text NOT NULL,
    input_tokens bigint NOT NULL DEFAULT 0,
    cached_input_tokens bigint NOT NULL DEFAULT 0,
    output_tokens bigint NOT NULL DEFAULT 0,
    audio_input_seconds numeric(12,3) NOT NULL DEFAULT 0,
    audio_output_seconds numeric(12,3) NOT NULL DEFAULT 0,
    estimated_cost_usd numeric(18,9) NOT NULL,
    price_catalog_id uuid NOT NULL REFERENCES ai_price_catalog(id),
    latency_ms bigint NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX ai_usage_owner_report ON ai_usage_records (created_at, purpose, model);

CREATE TABLE speech_inputs (
    id uuid PRIMARY KEY,
    student_id uuid NOT NULL REFERENCES students(id),
    session_id uuid REFERENCES learning_sessions(id),
    provider text NOT NULL,
    model text NOT NULL,
    transcript text,
    duration_seconds numeric(12,3) NOT NULL,
    storage_status text NOT NULL CHECK (storage_status IN ('PROCESSING', 'DELETED', 'RETAINED_WITH_CONSENT')),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE speech_outputs (
    id uuid PRIMARY KEY,
    student_id uuid NOT NULL REFERENCES students(id),
    session_id uuid REFERENCES learning_sessions(id),
    provider text NOT NULL,
    model text NOT NULL,
    duration_seconds numeric(12,3) NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE speech_segments (
    id uuid PRIMARY KEY,
    speech_output_id uuid NOT NULL REFERENCES speech_outputs(id) ON DELETE CASCADE,
    sequence integer NOT NULL,
    text text NOT NULL,
    start_ms integer NOT NULL CHECK (start_ms >= 0),
    end_ms integer NOT NULL CHECK (end_ms > start_ms),
    UNIQUE (speech_output_id, sequence)
);
