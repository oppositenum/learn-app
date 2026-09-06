ALTER TABLE review_queue
ADD COLUMN resolved_at timestamptz,
ADD COLUMN result text CHECK (result IN ('INDEPENDENT_SUCCESS', 'ASSISTED_SUCCESS', 'FAILED', 'CANCELLED'));

UPDATE review_queue
SET result = CASE status
        WHEN 'COMPLETED' THEN 'INDEPENDENT_SUCCESS'
        WHEN 'CANCELLED' THEN 'CANCELLED'
    END,
    resolved_at = due_at
WHERE status IN ('COMPLETED', 'CANCELLED');

WITH ranked AS (
    SELECT id,
           row_number() OVER (
               PARTITION BY student_id, knowledge_point_id, source
               ORDER BY due_at, priority DESC, created_at, id
           ) AS position
    FROM review_queue
    WHERE status = 'PENDING'
)
UPDATE review_queue queue
SET status = 'CANCELLED', result = 'CANCELLED', resolved_at = now()
FROM ranked
WHERE queue.id = ranked.id AND ranked.position > 1;

ALTER TABLE review_queue
ADD CONSTRAINT review_queue_resolution_consistent CHECK (
    (status = 'PENDING' AND result IS NULL AND resolved_at IS NULL)
    OR
    (status IN ('COMPLETED', 'CANCELLED') AND result IS NOT NULL AND resolved_at IS NOT NULL)
);

CREATE UNIQUE INDEX review_queue_one_pending_source
ON review_queue(student_id, knowledge_point_id, source)
WHERE status = 'PENDING';

ALTER TABLE learning_plan_blocks
ADD COLUMN review_queue_id uuid REFERENCES review_queue(id) ON DELETE SET NULL;

ALTER TABLE learning_sessions
ADD COLUMN review_queue_id uuid REFERENCES review_queue(id) ON DELETE SET NULL,
ADD COLUMN review_attempt_failed_at timestamptz;

INSERT INTO review_queue(id, student_id, knowledge_point_id, source, due_at, priority)
SELECT DISTINCT ON (plan.student_id, block.knowledge_point_id)
       gen_random_uuid(), plan.student_id, block.knowledge_point_id, 'PARENT_PRIORITY',
       plan.plan_date::timestamptz, 70
FROM learning_plan_blocks block
JOIN learning_plans plan ON plan.id = block.plan_id
WHERE block.mode = 'REVIEW'
  AND block.reason = 'parent_review_only'
  AND block.status IN ('AVAILABLE', 'ACTIVE')
  AND plan.status IN ('PROPOSED', 'ACTIVE')
  AND block.knowledge_point_id IS NOT NULL
ORDER BY plan.student_id, block.knowledge_point_id, plan.plan_date, block.id
ON CONFLICT(student_id, knowledge_point_id, source) WHERE status = 'PENDING'
DO UPDATE SET due_at = LEAST(review_queue.due_at, EXCLUDED.due_at),
              priority = GREATEST(review_queue.priority, EXCLUDED.priority);

WITH bindings AS (
    SELECT block.id AS block_id,
           (
               SELECT queue.id
               FROM review_queue queue
               WHERE queue.student_id = plan.student_id
                 AND queue.knowledge_point_id = block.knowledge_point_id
                 AND queue.status = 'PENDING'
                 AND queue.due_at < (plan.plan_date + 1)::timestamptz
               ORDER BY
                   CASE
                       WHEN block.reason = 'parent_review_only' AND queue.source = 'PARENT_PRIORITY' THEN 0
                       ELSE 1
                   END,
                   queue.due_at, queue.priority DESC, queue.created_at, queue.id
               LIMIT 1
           ) AS queue_id
    FROM learning_plan_blocks block
    JOIN learning_plans plan ON plan.id = block.plan_id
    WHERE block.mode = 'REVIEW'
      AND block.status IN ('AVAILABLE', 'ACTIVE')
      AND plan.status IN ('PROPOSED', 'ACTIVE')
)
UPDATE learning_plan_blocks block
SET review_queue_id = bindings.queue_id
FROM bindings
WHERE block.id = bindings.block_id AND bindings.queue_id IS NOT NULL;

UPDATE learning_sessions session
SET review_queue_id = block.review_queue_id
FROM learning_plan_blocks block
WHERE block.id = session.plan_block_id
  AND block.mode = 'REVIEW'
  AND block.review_queue_id IS NOT NULL
  AND session.status IN ('ACTIVE', 'PAUSED');

CREATE INDEX learning_plan_blocks_review_queue
ON learning_plan_blocks(review_queue_id)
WHERE review_queue_id IS NOT NULL;

CREATE INDEX learning_sessions_review_queue
ON learning_sessions(review_queue_id, started_at DESC)
WHERE review_queue_id IS NOT NULL;
