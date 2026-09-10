ALTER TABLE classroom_stage_attempts
    DROP CONSTRAINT classroom_stage_attempts_response_code_check,
    ADD CONSTRAINT classroom_stage_attempts_response_code_check CHECK (response_code IN (
        'TRY_NEW_TASK',
        'HELP_DELIVERED',
        'ASSISTED_REPROOF',
        'SOCRATIC_LIMIT_EXPLAINED',
        'SOCRATIC_REPROOF_FAILED',
        'NEXT_STAGE',
        'CLASSROOM_COMPLETE',
        'CONTENT_EXHAUSTED'
    ));
