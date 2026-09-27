-- The knowledge domain a parent wants the next plan to lean toward. It names a
-- domain, never a knowledge point or a question, and it is optional.
ALTER TABLE parent_preferences
ADD COLUMN priority_domain_id uuid REFERENCES domains(id);
