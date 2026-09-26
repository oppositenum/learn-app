-- A stage classroom takes a break on the same task when a wrong answer carries
-- a fixed emotion signal. That attempt answers with action BREAK and code
-- EMOTION_BREAK. Existing rows are unchanged.
ALTER TABLE classroom_stage_attempts
    DROP CONSTRAINT classroom_stage_attempts_response_code_check,
    ADD CONSTRAINT classroom_stage_attempts_response_code_check CHECK (response_code IN (
        'TRY_NEW_TASK',
        'SOCRATIC_GUIDED',
        'EMOTION_BREAK',
        'HELP_DELIVERED',
        'ASSISTED_REPROOF',
        'SOCRATIC_LIMIT_EXPLAINED',
        'SOCRATIC_REPROOF_FAILED',
        'NEXT_STAGE',
        'CLASSROOM_COMPLETE',
        'CONTENT_EXHAUSTED'
    ));

ALTER TABLE classroom_stage_attempts
    DROP CONSTRAINT classroom_stage_attempts_response_action_check,
    ADD CONSTRAINT classroom_stage_attempts_response_action_check CHECK (response_action IN (
        'ORIGINAL',
        'VARIANT',
        'ABSTRACT',
        'VERIFY',
        'COMPLETE',
        'PROBE',
        'SCAFFOLD',
        'ANALOGY',
        'HINT',
        'EXPLAIN',
        'BREAK'
    ));
