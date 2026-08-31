-- Reconstruct the complete draft assets for the curated V1 seed set. The
-- original release evidence remains explicitly curated; automated tests load
-- these assets and run the same deterministic validator used by Owner HTTP.
UPDATE content_versions cv
SET asset_json = jsonb_build_object(
    'question_id', q.id::text,
    'knowledge_point_id', q.knowledge_point_id::text,
    'subject_code', s.code,
    'difficulty', q.difficulty,
    'question_type', q.question_type,
    'prompt_public', q.prompt_public,
    'teacher_private', jsonb_build_object(
        'answer', a.teacher_reference_answer,
        'solution', a.teacher_reference_answer,
        'misconceptions', a.misconceptions_private_json
    ),
    'input_schema', q.input_schema_json,
    'source_id', cv.source_id::text,
    'content_version', cv.version,
    'schema_version', cv.schema_version,
    'status', 'DRAFT'
)
FROM questions q
JOIN question_private_answers a ON a.question_id = q.id
JOIN knowledge_points kp ON kp.id = q.knowledge_point_id
JOIN subjects s ON s.id = kp.subject_id
WHERE cv.question_id = q.id
  AND q.id::text LIKE '40000000-0000-4000-8000-0000000000%'
  AND cv.generator_provider = 'internal'
  AND cv.generator_model = 'curated-v1';
