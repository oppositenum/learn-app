ALTER TABLE learning_sessions
ADD COLUMN evidence_form text NOT NULL DEFAULT 'LIFE'
CHECK (evidence_form IN ('LIFE','VARIANT','TEXTBOOK','REVIEW'));
