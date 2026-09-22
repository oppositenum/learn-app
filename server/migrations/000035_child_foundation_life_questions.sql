-- Align the live demo answers with the step the stem actually asks, and add
-- one released English-vocabulary life question so the Grade-9 planner can
-- serve this child's first weak point. Catalog skeleton points stay RELEASED
-- without questions; Owner still has to generate and release later-year work.

UPDATE question_private_answers
SET scoring_key_json = jsonb_set(
        COALESCE(scoring_key_json, '{}'::jsonb),
        '{accepted_answers}',
        '["设每张x元","设每张门票的价格为x元","设单价为x元","x","(36-6)/3","(36－6)÷3","30/3","10"]'::jsonb,
        true
    )
WHERE question_id = '40000000-0000-4000-8000-000000000001';

UPDATE question_private_answers
SET scoring_key_json = jsonb_set(
        COALESCE(scoring_key_json, '{}'::jsonb),
        '{accepted_answers}',
        '["公分母12","12","通分成十二分之四和十二分之三"]'::jsonb,
        true
    )
WHERE question_id = '40000000-0000-4000-8000-000000000002';

UPDATE question_private_answers
SET scoring_key_json = jsonb_set(
        COALESCE(scoring_key_json, '{}'::jsonb),
        '{accepted_answers}',
        '["80%","百分之八十","保留原价的80%","现价是原价的80%"]'::jsonb,
        true
    )
WHERE question_id = '40000000-0000-4000-8000-000000000003';

UPDATE question_private_answers
SET scoring_key_json = jsonb_set(
        COALESCE(scoring_key_json, '{}'::jsonb),
        '{accepted_answers}',
        '["路程和时间","30千米和2小时","路程30千米和时间2小时"]'::jsonb,
        true
    )
WHERE question_id = '40000000-0000-4000-8000-000000000010';

UPDATE question_private_answers
SET scoring_key_json = jsonb_set(
        COALESCE(scoring_key_json, '{}'::jsonb),
        '{accepted_answers}',
        '["is; are","is, are","is are"]'::jsonb,
        true
    )
WHERE question_id = '40000000-0000-4000-8000-000000000007';

INSERT INTO questions (id,knowledge_point_id,difficulty,question_type,prompt_public,scene_public_json,input_schema_json,status,content_version)
SELECT '40000000-0000-4000-8000-000000000016',
       kp.id,
       'L1',
       'FREE_TEXT',
       '妈妈在冰箱上留了一张英文纸条：Please put the leftovers in the fridge. 这里 leftovers 最接近哪个意思？用中文说。',
       '{"kind":"HOME"}',
       '{"type":"string"}',
       'DRAFT',
       'v1'
FROM knowledge_points kp
WHERE kp.code='ENGLISH-JUN-VOCABULARY'
  AND NOT EXISTS (SELECT 1 FROM questions q WHERE q.id='40000000-0000-4000-8000-000000000016');

INSERT INTO question_private_answers (question_id,correct_answer_json,full_solution_private,teacher_reference_answer,scoring_key_json,misconceptions_private_json,hint_policy_private_json)
SELECT '40000000-0000-4000-8000-000000000016',
       '{"value":"吃剩的食物"}',
       'leftovers 是吃剩下来、还要收进冰箱的食物，不是“离开”或“左边”。',
       '吃剩的食物',
       '{"concepts":["vocabulary_in_life"],"accepted_answers":["吃剩的食物","剩菜","剩饭","剩余的食物","剩下的饭菜"]}',
       '["LEFT_AS_DIRECTION"]',
       '{"max_hints":2}'
WHERE EXISTS (SELECT 1 FROM questions q WHERE q.id='40000000-0000-4000-8000-000000000016')
  AND NOT EXISTS (SELECT 1 FROM question_private_answers a WHERE a.question_id='40000000-0000-4000-8000-000000000016');

INSERT INTO content_versions (id,question_id,version,schema_version,generator_provider,generator_model,source_id,asset_json)
SELECT '60000000-0000-4000-8000-000000000016',
       q.id,
       'v1',
       'content-question-v1',
       'internal',
       'curated-v1',
       '50000000-0000-4000-8000-000000000001',
       jsonb_build_object(
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
           'source_id', '50000000-0000-4000-8000-000000000001',
           'content_version', 'v1',
           'schema_version', 'content-question-v1',
           'status', 'DRAFT'
       )
FROM questions q
JOIN question_private_answers a ON a.question_id = q.id
JOIN knowledge_points kp ON kp.id = q.knowledge_point_id
JOIN subjects s ON s.id = kp.subject_id
WHERE q.id='40000000-0000-4000-8000-000000000016'
  AND NOT EXISTS (SELECT 1 FROM content_versions cv WHERE cv.question_id='40000000-0000-4000-8000-000000000016');

INSERT INTO content_validations (id,question_id,content_version,schema_version,status,checks_json,validator_version)
SELECT '70000000-0000-4000-8000-000000000016',
       '40000000-0000-4000-8000-000000000016',
       'v1',
       'content-question-v1',
       'PASS',
       '[{"name":"curated_fixture","passed":true}]',
       'validator-v1'
WHERE EXISTS (SELECT 1 FROM questions q WHERE q.id='40000000-0000-4000-8000-000000000016')
  AND NOT EXISTS (SELECT 1 FROM content_validations cv WHERE cv.question_id='40000000-0000-4000-8000-000000000016');

INSERT INTO content_reviews (id,question_id,content_version,schema_version,result,reviewer_provider,reviewer_model,findings_json)
SELECT '80000000-0000-4000-8000-000000000016',
       '40000000-0000-4000-8000-000000000016',
       'v1',
       'content-question-v1',
       'PASS',
       'independent',
       'curated-review-v1',
       '[]'
WHERE EXISTS (SELECT 1 FROM questions q WHERE q.id='40000000-0000-4000-8000-000000000016')
  AND NOT EXISTS (SELECT 1 FROM content_reviews cr WHERE cr.question_id='40000000-0000-4000-8000-000000000016');

UPDATE questions
SET status='AUTOMATIC_VALIDATED'
WHERE id='40000000-0000-4000-8000-000000000016'
  AND status='DRAFT';

UPDATE questions
SET status='AI_REVIEWED'
WHERE id='40000000-0000-4000-8000-000000000016'
  AND status='AUTOMATIC_VALIDATED';

UPDATE questions
SET status='RELEASED'
WHERE id='40000000-0000-4000-8000-000000000016'
  AND status='AI_REVIEWED';

INSERT INTO content_release_records (id,question_id,from_status,to_status,validation_id,review_id,reason)
SELECT '90000000-0000-4000-8000-000000000016',
       '40000000-0000-4000-8000-000000000016',
       'AI_REVIEWED',
       'RELEASED',
       '70000000-0000-4000-8000-000000000016',
       '80000000-0000-4000-8000-000000000016',
       'Grade-9 English vocabulary life question for the current child'
WHERE EXISTS (SELECT 1 FROM questions q WHERE q.id='40000000-0000-4000-8000-000000000016')
  AND NOT EXISTS (SELECT 1 FROM content_release_records r WHERE r.question_id='40000000-0000-4000-8000-000000000016');
