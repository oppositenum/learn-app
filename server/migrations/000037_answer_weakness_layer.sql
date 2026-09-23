-- Record which layer a wrong answer is stuck at, L1 to L6. Rows written
-- before this migration stay NULL; there is no backfill and no default,
-- because a guessed layer would steer the next explanation on a guess.
-- A correct answer never carries a layer.

ALTER TABLE answer_analyses
    ADD COLUMN weakness_layer text,
    ADD CONSTRAINT answer_analyses_weakness_layer_known
        CHECK (weakness_layer IS NULL OR weakness_layer IN ('L1','L2','L3','L4','L5','L6')),
    ADD CONSTRAINT answer_analyses_correct_answer_has_no_weakness_layer
        CHECK (NOT answer_correct OR weakness_layer IS NULL);
