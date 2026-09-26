-- A stage classroom now keeps a wrong answer on the same task for three
-- Socratic rounds (PROBE, SCAFFOLD, ANALOGY) before the limit explanation.
-- Those rounds answer with SOCRATIC_GUIDED. TRY_NEW_TASK stays allowed so
-- rows written before this migration still replay.
ALTER TABLE classroom_stage_attempts
    DROP CONSTRAINT classroom_stage_attempts_response_code_check,
    ADD CONSTRAINT classroom_stage_attempts_response_code_check CHECK (response_code IN (
        'TRY_NEW_TASK',
        'SOCRATIC_GUIDED',
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
        'EXPLAIN'
    ));
