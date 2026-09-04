ALTER TABLE learning_sessions
ADD COLUMN plan_block_id uuid REFERENCES learning_plan_blocks(id) ON DELETE SET NULL,
ADD COLUMN accumulated_seconds integer NOT NULL DEFAULT 0 CHECK (accumulated_seconds >= 0),
ADD COLUMN last_resumed_at timestamptz,
ADD COLUMN last_activity_at timestamptz NOT NULL DEFAULT now(),
ADD COLUMN assistance_level smallint NOT NULL DEFAULT 0 CHECK (assistance_level BETWEEN 0 AND 4),
ADD COLUMN timing_version bigint NOT NULL DEFAULT 0 CHECK (timing_version >= 0),
ADD COLUMN processing_token uuid,
ADD COLUMN processing_until timestamptz;

ALTER TABLE learning_plan_blocks
ADD COLUMN status text NOT NULL DEFAULT 'AVAILABLE'
CHECK (status IN ('AVAILABLE', 'ACTIVE', 'COMPLETED'));

UPDATE learning_sessions
SET accumulated_seconds = actual_seconds,
    last_activity_at = COALESCE(ended_at, started_at),
    last_resumed_at = NULL
WHERE status IN ('COMPLETED', 'ABANDONED', 'PAUSED');

-- Existing ACTIVE rows predate reliable activity tracking. Preserve them as
-- resumable sessions without counting unknown wall-clock time as study time.
UPDATE learning_sessions
SET status = 'PAUSED',
    accumulated_seconds = actual_seconds,
    last_activity_at = started_at,
    last_resumed_at = NULL
WHERE status = 'ACTIVE';

UPDATE learning_sessions session
SET assistance_level = GREATEST(
    CASE WHEN session.socratic_fail_count > 0 THEN 1 ELSE 0 END,
    CASE session.current_state
        WHEN 'PROBE' THEN 1
        WHEN 'HINT' THEN 1
        WHEN 'SCAFFOLD' THEN 2
        WHEN 'ANALOGY' THEN 3
        WHEN 'BACKTRACK' THEN 3
        WHEN 'EXPLAIN' THEN 4
        WHEN 'VOICE_EXPLAIN' THEN 4
        ELSE 0
    END,
    COALESCE((
        SELECT max(CASE turn.action
            WHEN 'PROBE' THEN 1
            WHEN 'HINT' THEN 1
            WHEN 'SCAFFOLD' THEN 2
            WHEN 'ANALOGY' THEN 3
            WHEN 'BACKTRACK' THEN 3
            WHEN 'EXPLAIN' THEN 4
            WHEN 'VOICE_EXPLAIN' THEN 4
            ELSE 0
        END)
        FROM tutor_turns turn
        WHERE turn.session_id = session.id
    ), 0)
);

-- Link only sessions with one unambiguous same-day plan block. Ambiguous
-- historical rows remain NULL instead of guessing and corrupting plan state.
WITH candidates AS (
    SELECT session.id AS session_id,(array_agg(block.id ORDER BY block.id))[1] AS block_id,count(*) AS matches
    FROM learning_sessions session
    JOIN questions question ON question.id=session.current_question_id
    JOIN learning_plans plan ON plan.student_id=session.student_id
        AND plan.plan_date=(session.started_at AT TIME ZONE 'Asia/Shanghai')::date
        AND plan.status IN ('PROPOSED','ACTIVE','COMPLETED')
    JOIN learning_plan_blocks block ON block.plan_id=plan.id
        AND (
            (session.original_task_id IS NULL
                AND block.original_task_id IS NULL
                AND block.subject_id=session.subject_id
                AND block.knowledge_point_id=question.knowledge_point_id)
            OR
            (session.original_task_id IS NOT NULL AND (
                (block.original_task_id IS NULL AND block.knowledge_point_id=session.original_task_id)
                OR block.original_task_id=session.original_task_id
            ))
        )
    GROUP BY session.id
)
UPDATE learning_sessions session
SET plan_block_id=candidates.block_id
FROM candidates
WHERE session.id=candidates.session_id AND candidates.matches=1;

WITH ranked AS (
    SELECT id,
           row_number() OVER (
               PARTITION BY student_id
               ORDER BY started_at DESC, id DESC
           ) AS position
    FROM learning_sessions
    WHERE status IN ('ACTIVE', 'PAUSED')
)
UPDATE learning_sessions session
SET status = 'ABANDONED',
    ended_at = COALESCE(session.ended_at, session.last_activity_at),
    actual_seconds = session.accumulated_seconds,
    last_resumed_at = NULL
FROM ranked
WHERE session.id = ranked.id AND ranked.position > 1;

UPDATE learning_plan_blocks block
SET status=CASE
    WHEN EXISTS (SELECT 1 FROM learning_sessions session WHERE session.plan_block_id=block.id AND session.status IN ('ACTIVE','PAUSED')) THEN 'ACTIVE'
    WHEN EXISTS (SELECT 1 FROM learning_sessions session WHERE session.plan_block_id=block.id AND session.status='COMPLETED') THEN 'COMPLETED'
    ELSE 'AVAILABLE'
END
WHERE EXISTS (SELECT 1 FROM learning_sessions session WHERE session.plan_block_id=block.id);

DROP INDEX learning_sessions_student_active;

CREATE UNIQUE INDEX learning_sessions_one_open_per_student
ON learning_sessions(student_id)
WHERE status IN ('ACTIVE', 'PAUSED');

CREATE INDEX learning_sessions_stale_active
ON learning_sessions(last_activity_at)
WHERE status = 'ACTIVE';

CREATE INDEX learning_sessions_stale_paused
ON learning_sessions(last_activity_at)
WHERE status = 'PAUSED';

CREATE INDEX learning_sessions_plan_block
ON learning_sessions(plan_block_id, started_at DESC)
WHERE plan_block_id IS NOT NULL;
