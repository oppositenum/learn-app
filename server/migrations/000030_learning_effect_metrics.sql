CREATE TABLE learning_effect_events (
    id uuid PRIMARY KEY,
    student_id uuid NOT NULL REFERENCES students(id),
    session_id uuid NOT NULL REFERENCES learning_sessions(id) ON DELETE RESTRICT,
    subject_id uuid REFERENCES subjects(id),
    knowledge_point_id uuid REFERENCES knowledge_points(id),
    question_id uuid REFERENCES questions(id),
    event_type text NOT NULL CHECK (event_type IN (
        'TASK_ATTEMPT',
        'SUPPORT_REQUESTED',
        'TUTOR_OUTPUT',
        'SESSION_EXIT'
    )),
    source_kind text NOT NULL CHECK (source_kind IN (
        'ANSWER_ANALYSIS',
        'STAGE_ATTEMPT',
        'TUTOR_TURN',
        'LEARNING_SESSION'
    )),
    source_id uuid NOT NULL,
    outcome text NOT NULL CHECK (outcome IN (
        'CORRECT',
        'INCORRECT',
        'INDETERMINATE',
        'HELP_REQUESTED',
        'DELIVERED',
        'COMPLETED',
        'ABANDONED'
    )),
    support_type text CHECK (support_type IS NULL OR support_type IN ('HINT', 'EXPLAIN')),
    classroom_state text CHECK (
        classroom_state IS NULL OR classroom_state IN (
            'INTRO', 'ASK', 'WAIT', 'ANALYZE', 'PROBE', 'HINT', 'SCAFFOLD',
            'ANALOGY', 'BACKTRACK', 'EXPLAIN', 'VOICE_EXPLAIN', 'RETURN',
            'ORIGINAL', 'VARIANT', 'ABSTRACT', 'VERIFY', 'REVIEW', 'COMPLETE', 'BREAK'
        )
    ),
    evidence_form text CHECK (
        evidence_form IS NULL OR evidence_form IN ('LIFE', 'VARIANT', 'TEXTBOOK', 'REVIEW')
    ),
    assistance_level smallint NOT NULL CHECK (assistance_level BETWEEN 0 AND 4),
    effective_elapsed_ms bigint CHECK (effective_elapsed_ms IS NULL OR effective_elapsed_ms >= 0),
    review_interval_days smallint CHECK (review_interval_days IS NULL OR review_interval_days > 0),
    classification_version text NOT NULL DEFAULT 'learning-effect-event-v1'
        CHECK (classification_version = 'learning-effect-event-v1'),
    occurred_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (event_type, source_kind, source_id),
    CHECK ((event_type = 'SUPPORT_REQUESTED') = (support_type IS NOT NULL))
);

CREATE INDEX learning_effect_events_student_time
    ON learning_effect_events(student_id, occurred_at DESC);

CREATE INDEX learning_effect_events_metrics
    ON learning_effect_events(event_type, occurred_at, classroom_state, outcome);

COMMENT ON TABLE learning_effect_events IS
    'Append-only learning-effect classifications and timing. Answer bodies, Tutor prose, structured responses, and audio are intentionally absent.';

CREATE TABLE ai_request_outcomes (
    request_id text PRIMARY KEY CHECK (length(request_id) BETWEEN 1 AND 128),
    student_id uuid REFERENCES students(id),
    session_id uuid REFERENCES learning_sessions(id) ON DELETE RESTRICT,
    provider text NOT NULL CHECK (length(provider) BETWEEN 1 AND 64),
    model text NOT NULL CHECK (length(model) BETWEEN 1 AND 128),
    purpose text NOT NULL CHECK (length(purpose) BETWEEN 1 AND 64),
    outcome text NOT NULL CHECK (outcome IN (
        'SUCCEEDED',
        'PROVIDER_ERROR',
        'TRANSPORT_ERROR',
        'INVALID_RESPONSE',
        'ACCOUNTING_ERROR'
    )),
    http_status integer CHECK (http_status IS NULL OR http_status BETWEEN 100 AND 599),
    latency_ms bigint NOT NULL CHECK (latency_ms >= 0),
    occurred_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX ai_request_outcomes_metrics
    ON ai_request_outcomes(occurred_at, purpose, outcome);

COMMENT ON TABLE ai_request_outcomes IS
    'Append-only structured AI request outcomes. Provider responses, prompts, model prose, and error bodies are intentionally absent.';

CREATE OR REPLACE FUNCTION reject_learning_metrics_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'learning metric records are append-only' USING ERRCODE = '55000';
END
$$;

CREATE TRIGGER learning_effect_events_append_only
BEFORE UPDATE OR DELETE ON learning_effect_events
FOR EACH ROW EXECUTE FUNCTION reject_learning_metrics_mutation();

CREATE TRIGGER ai_request_outcomes_append_only
BEFORE UPDATE OR DELETE ON ai_request_outcomes
FOR EACH ROW EXECUTE FUNCTION reject_learning_metrics_mutation();

CREATE OR REPLACE FUNCTION learning_effect_review_interval(review_id uuid) RETURNS smallint
LANGUAGE sql STABLE AS $$
    SELECT CASE
        WHEN review_id IS NULL THEN NULL
        ELSE GREATEST(1, round(EXTRACT(EPOCH FROM (due_at - created_at)) / 86400.0)::integer)::smallint
    END
    FROM review_queue
    WHERE id = review_id
$$;

CREATE OR REPLACE FUNCTION capture_answer_analysis_learning_effect() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO learning_effect_events(
        id, student_id, session_id, subject_id, knowledge_point_id, question_id,
        event_type, source_kind, source_id, outcome, classroom_state, evidence_form,
        assistance_level, effective_elapsed_ms, review_interval_days, occurred_at
    )
    SELECT gen_random_uuid(), session.student_id, session.id, session.subject_id,
           question.knowledge_point_id, answer.question_id,
           'TASK_ATTEMPT', 'ANSWER_ANALYSIS', NEW.id,
           CASE WHEN NEW.answer_correct THEN 'CORRECT' ELSE 'INCORRECT' END,
           session.current_state, session.evidence_form, session.assistance_level,
           1000::bigint * GREATEST(0::bigint,
               session.accumulated_seconds::bigint + CASE
                   WHEN session.status = 'ACTIVE' THEN GREATEST(
                       0,
                       EXTRACT(EPOCH FROM (answer.submitted_at - COALESCE(session.last_resumed_at, session.started_at)))::integer
                   )
                   ELSE 0
               END
           ),
           learning_effect_review_interval(session.review_queue_id),
           answer.submitted_at
    FROM student_answers answer
    JOIN learning_sessions session ON session.id = answer.session_id
    JOIN questions question ON question.id = answer.question_id
    WHERE answer.id = NEW.student_answer_id
    ON CONFLICT (event_type, source_kind, source_id) DO NOTHING;
    RETURN NEW;
END
$$;

CREATE TRIGGER answer_analysis_learning_effect
AFTER INSERT ON answer_analyses
FOR EACH ROW EXECUTE FUNCTION capture_answer_analysis_learning_effect();

CREATE OR REPLACE FUNCTION capture_stage_attempt_learning_effect() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO learning_effect_events(
        id, student_id, session_id, subject_id, knowledge_point_id, question_id,
        event_type, source_kind, source_id, outcome, support_type, classroom_state,
        evidence_form, assistance_level, effective_elapsed_ms, review_interval_days, occurred_at
    )
    SELECT gen_random_uuid(), session.student_id, session.id, session.subject_id,
           stage_session.knowledge_point_id, NEW.question_id,
           CASE WHEN NEW.attempt_kind = 'HELP' THEN 'SUPPORT_REQUESTED' ELSE 'TASK_ATTEMPT' END,
           'STAGE_ATTEMPT', NEW.id,
           CASE
               WHEN NEW.attempt_kind = 'HELP' THEN 'HELP_REQUESTED'
               ELSE NEW.deterministic_result
           END,
           NEW.support_type, NEW.submitted_stage, stage_task.evidence_form,
           session.assistance_level, NEW.response_active_seconds::bigint * 1000,
           learning_effect_review_interval(session.review_queue_id), NEW.created_at
    FROM learning_sessions session
    JOIN classroom_stage_sessions stage_session ON stage_session.session_id = session.id
    JOIN classroom_stage_tasks stage_task ON stage_task.question_id = NEW.question_id
    WHERE session.id = NEW.session_id
    ON CONFLICT (event_type, source_kind, source_id) DO NOTHING;
    RETURN NEW;
END
$$;

CREATE TRIGGER stage_attempt_learning_effect
AFTER INSERT ON classroom_stage_attempts
FOR EACH ROW EXECUTE FUNCTION capture_stage_attempt_learning_effect();

CREATE OR REPLACE FUNCTION capture_tutor_output_learning_effect() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.actor <> 'TUTOR' THEN
        RETURN NEW;
    END IF;
    INSERT INTO learning_effect_events(
        id, student_id, session_id, subject_id, knowledge_point_id, question_id,
        event_type, source_kind, source_id, outcome, classroom_state, evidence_form,
        assistance_level, occurred_at
    )
    SELECT gen_random_uuid(), session.student_id, session.id, session.subject_id,
           question.knowledge_point_id, session.current_question_id,
           'TUTOR_OUTPUT', 'TUTOR_TURN', NEW.id, 'DELIVERED',
           COALESCE(NEW.action, session.current_state), session.evidence_form,
           session.assistance_level, NEW.created_at
    FROM learning_sessions session
    LEFT JOIN questions question ON question.id = session.current_question_id
    WHERE session.id = NEW.session_id
    ON CONFLICT (event_type, source_kind, source_id) DO NOTHING;
    RETURN NEW;
END
$$;

CREATE TRIGGER tutor_output_learning_effect
AFTER INSERT ON tutor_turns
FOR EACH ROW EXECUTE FUNCTION capture_tutor_output_learning_effect();

CREATE OR REPLACE FUNCTION capture_session_exit_learning_effect() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.status NOT IN ('COMPLETED', 'ABANDONED')
       OR OLD.status IN ('COMPLETED', 'ABANDONED') THEN
        RETURN NEW;
    END IF;
    INSERT INTO learning_effect_events(
        id, student_id, session_id, subject_id, knowledge_point_id, question_id,
        event_type, source_kind, source_id, outcome, classroom_state, evidence_form,
        assistance_level, effective_elapsed_ms, review_interval_days, occurred_at
    )
    SELECT gen_random_uuid(), NEW.student_id, NEW.id, NEW.subject_id,
           question.knowledge_point_id, NEW.current_question_id,
           'SESSION_EXIT', 'LEARNING_SESSION', NEW.id, NEW.status,
           NEW.current_state, NEW.evidence_form, NEW.assistance_level,
           NEW.actual_seconds::bigint * 1000,
           learning_effect_review_interval(NEW.review_queue_id),
           COALESCE(NEW.ended_at, CURRENT_TIMESTAMP)
    FROM (SELECT 1) seed
    LEFT JOIN questions question ON question.id = NEW.current_question_id
    ON CONFLICT (event_type, source_kind, source_id) DO NOTHING;
    RETURN NEW;
END
$$;

CREATE TRIGGER session_exit_learning_effect
AFTER UPDATE OF status ON learning_sessions
FOR EACH ROW EXECUTE FUNCTION capture_session_exit_learning_effect();

-- Existing records receive classifications and identifiers only. Effective latency is
-- intentionally NULL where the historical active-time checkpoint cannot be reconstructed.
INSERT INTO learning_effect_events(
    id, student_id, session_id, subject_id, knowledge_point_id, question_id,
    event_type, source_kind, source_id, outcome, classroom_state, evidence_form,
    assistance_level, effective_elapsed_ms, review_interval_days, occurred_at
)
SELECT gen_random_uuid(), session.student_id, session.id, session.subject_id,
       question.knowledge_point_id, answer.question_id,
       'TASK_ATTEMPT', 'ANSWER_ANALYSIS', analysis.id,
       CASE WHEN analysis.answer_correct THEN 'CORRECT' ELSE 'INCORRECT' END,
       NULL, session.evidence_form, session.assistance_level, NULL,
       learning_effect_review_interval(session.review_queue_id), answer.submitted_at
FROM answer_analyses analysis
JOIN student_answers answer ON answer.id = analysis.student_answer_id
JOIN learning_sessions session ON session.id = answer.session_id
JOIN questions question ON question.id = answer.question_id
ON CONFLICT (event_type, source_kind, source_id) DO NOTHING;

INSERT INTO learning_effect_events(
    id, student_id, session_id, subject_id, knowledge_point_id, question_id,
    event_type, source_kind, source_id, outcome, support_type, classroom_state,
    evidence_form, assistance_level, effective_elapsed_ms, review_interval_days, occurred_at
)
SELECT gen_random_uuid(), session.student_id, session.id, session.subject_id,
       stage_session.knowledge_point_id, attempt.question_id,
       CASE WHEN attempt.attempt_kind = 'HELP' THEN 'SUPPORT_REQUESTED' ELSE 'TASK_ATTEMPT' END,
       'STAGE_ATTEMPT', attempt.id,
       CASE WHEN attempt.attempt_kind = 'HELP' THEN 'HELP_REQUESTED' ELSE attempt.deterministic_result END,
       attempt.support_type, attempt.submitted_stage, stage_task.evidence_form,
       session.assistance_level, attempt.response_active_seconds::bigint * 1000,
       learning_effect_review_interval(session.review_queue_id), attempt.created_at
FROM classroom_stage_attempts attempt
JOIN classroom_stage_sessions stage_session ON stage_session.session_id = attempt.session_id
JOIN learning_sessions session ON session.id = attempt.session_id
JOIN classroom_stage_tasks stage_task ON stage_task.question_id = attempt.question_id
ON CONFLICT (event_type, source_kind, source_id) DO NOTHING;

INSERT INTO learning_effect_events(
    id, student_id, session_id, subject_id, knowledge_point_id, question_id,
    event_type, source_kind, source_id, outcome, classroom_state, evidence_form,
    assistance_level, occurred_at
)
SELECT gen_random_uuid(), session.student_id, session.id, session.subject_id,
       question.knowledge_point_id, session.current_question_id,
       'TUTOR_OUTPUT', 'TUTOR_TURN', turn.id, 'DELIVERED',
       COALESCE(turn.action, session.current_state), session.evidence_form,
       session.assistance_level, turn.created_at
FROM tutor_turns turn
JOIN learning_sessions session ON session.id = turn.session_id
LEFT JOIN questions question ON question.id = session.current_question_id
WHERE turn.actor = 'TUTOR'
ON CONFLICT (event_type, source_kind, source_id) DO NOTHING;

INSERT INTO learning_effect_events(
    id, student_id, session_id, subject_id, knowledge_point_id, question_id,
    event_type, source_kind, source_id, outcome, classroom_state, evidence_form,
    assistance_level, effective_elapsed_ms, review_interval_days, occurred_at
)
SELECT gen_random_uuid(), session.student_id, session.id, session.subject_id,
       question.knowledge_point_id, session.current_question_id,
       'SESSION_EXIT', 'LEARNING_SESSION', session.id, session.status,
       session.current_state, session.evidence_form, session.assistance_level,
       session.actual_seconds::bigint * 1000,
       learning_effect_review_interval(session.review_queue_id), session.ended_at
FROM learning_sessions session
LEFT JOIN questions question ON question.id = session.current_question_id
WHERE session.status IN ('COMPLETED', 'ABANDONED') AND session.ended_at IS NOT NULL
ON CONFLICT (event_type, source_kind, source_id) DO NOTHING;
