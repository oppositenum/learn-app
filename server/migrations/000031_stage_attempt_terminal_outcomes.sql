ALTER TABLE classroom_stage_attempts
    DROP CONSTRAINT classroom_stage_attempts_response_code_check,
    ADD CONSTRAINT classroom_stage_attempts_response_code_check CHECK (response_code IN (
        'TRY_NEW_TASK',
        'HELP_DELIVERED',
        'ASSISTED_REPROOF',
        'SOCRATIC_LIMIT_EXPLAINED',
        'NEXT_STAGE',
        'CLASSROOM_COMPLETE',
        'CONTENT_EXHAUSTED'
    )),
    DROP CONSTRAINT classroom_stage_attempts_response_status_check,
    ADD CONSTRAINT classroom_stage_attempts_response_status_check CHECK (response_status IN (
        'ACTIVE',
        'PAUSED',
        'COMPLETED',
        'ABANDONED'
    )),
    ADD COLUMN response_socratic_round smallint NOT NULL DEFAULT 0
        CHECK (response_socratic_round BETWEEN 0 AND 3);

COMMENT ON COLUMN classroom_stage_attempts.response_socratic_round IS
    'The persisted failed-round count returned for an idempotent stage operation.';

ALTER TABLE classroom_stage_attempts
    ADD COLUMN response_action text;

UPDATE classroom_stage_attempts
SET response_action=response_stage;

ALTER TABLE classroom_stage_attempts
    ALTER COLUMN response_action SET NOT NULL,
    ADD CONSTRAINT classroom_stage_attempts_response_action_check CHECK (response_action IN (
        'ORIGINAL',
        'VARIANT',
        'ABSTRACT',
        'VERIFY',
        'COMPLETE',
        'PROBE',
        'HINT',
        'EXPLAIN'
    ));

COMMENT ON COLUMN classroom_stage_attempts.response_action IS
    'The persisted Student-visible action returned for an idempotent stage operation.';
