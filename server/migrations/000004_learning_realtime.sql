CREATE TABLE learning_sessions (
    id uuid PRIMARY KEY,
    student_id uuid NOT NULL REFERENCES students(id),
    subject_id uuid NOT NULL REFERENCES subjects(id),
    current_question_id uuid REFERENCES questions(id),
    started_at timestamptz NOT NULL DEFAULT now(),
    ended_at timestamptz,
    status text NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'PAUSED', 'COMPLETED', 'ABANDONED')),
    target_minutes smallint NOT NULL CHECK (target_minutes BETWEEN 1 AND 120),
    actual_seconds integer NOT NULL DEFAULT 0 CHECK (actual_seconds >= 0),
    current_state text NOT NULL,
    socratic_fail_count smallint NOT NULL DEFAULT 0 CHECK (socratic_fail_count BETWEEN 0 AND 3),
    engagement_state text NOT NULL DEFAULT 'NORMAL' CHECK (engagement_state IN ('HIGH', 'NORMAL', 'LOW')),
    original_task_id uuid,
    active_task_id uuid,
    teaching_response_id text,
    version bigint NOT NULL DEFAULT 1
);

CREATE INDEX learning_sessions_student_active ON learning_sessions (student_id, started_at DESC) WHERE status IN ('ACTIVE', 'PAUSED');

CREATE TABLE tutor_turns (
    id uuid PRIMARY KEY,
    session_id uuid NOT NULL REFERENCES learning_sessions(id) ON DELETE CASCADE,
    sequence bigint NOT NULL,
    actor text NOT NULL CHECK (actor IN ('STUDENT', 'TUTOR', 'SYSTEM')),
    action text,
    message text NOT NULL,
    reason_private text NOT NULL DEFAULT '',
    response_id text,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (session_id, sequence)
);

CREATE TABLE student_answers (
    id uuid PRIMARY KEY,
    session_id uuid NOT NULL REFERENCES learning_sessions(id) ON DELETE CASCADE,
    question_id uuid NOT NULL REFERENCES questions(id),
    turn_id uuid REFERENCES tutor_turns(id),
    answer_text text NOT NULL,
    client_elapsed_ms integer NOT NULL DEFAULT 0 CHECK (client_elapsed_ms >= 0),
    submitted_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE answer_analyses (
    id uuid PRIMARY KEY,
    student_answer_id uuid NOT NULL UNIQUE REFERENCES student_answers(id) ON DELETE CASCADE,
    answer_correct boolean NOT NULL,
    reasoning_quality text NOT NULL,
    confidence numeric(5,4) NOT NULL CHECK (confidence BETWEEN 0 AND 1),
    error_type text NOT NULL,
    misconceptions_private_json jsonb NOT NULL DEFAULT '[]'::jsonb,
    emotion_signal text NOT NULL,
    engagement text NOT NULL,
    recommended_action text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE tutor_events (
    id uuid PRIMARY KEY,
    session_id uuid NOT NULL REFERENCES learning_sessions(id) ON DELETE CASCADE,
    student_id uuid NOT NULL REFERENCES students(id),
    sequence bigint NOT NULL,
    type text NOT NULL,
    student_payload_json jsonb NOT NULL DEFAULT '{}'::jsonb,
    parent_payload_json jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (session_id, sequence)
);

CREATE INDEX tutor_events_parent_stream ON tutor_events (student_id, sequence);

CREATE TABLE parent_interventions (
    id uuid PRIMARY KEY,
    parent_user_id uuid NOT NULL REFERENCES users(id),
    student_id uuid NOT NULL REFERENCES students(id),
    session_id uuid REFERENCES learning_sessions(id),
    type text NOT NULL CHECK (type IN ('ENCOURAGEMENT', 'REDUCE_INTENSITY', 'REVIEW_ONLY', 'STATE_NOT_GOOD')),
    payload_json jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now()
);
