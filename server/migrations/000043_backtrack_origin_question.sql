-- The exact question a cross-subject backtrack left, so returning restores
-- that question rather than whichever released question of the original
-- knowledge point sorts first. The evidence form of that question is kept
-- with it; both are cleared once the classroom returns.
ALTER TABLE learning_sessions
ADD COLUMN backtrack_origin_question_id uuid REFERENCES questions(id),
ADD COLUMN backtrack_origin_evidence_form text
CHECK (backtrack_origin_evidence_form IN ('LIFE','VARIANT','TEXTBOOK','REVIEW'));
