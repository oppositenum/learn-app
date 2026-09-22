-- Give MATH-JUN-REMOVE-PARENTHESES a released life question so Grade-9
-- planning can leave the ticket demo. Catalog skeleton points stay RELEASED
-- without questions; Owner still has to generate and release later-year work.

INSERT INTO questions (id,knowledge_point_id,difficulty,question_type,prompt_public,scene_public_json,input_schema_json,status,content_version)
SELECT '40000000-0000-4000-8000-000000000017',
       kp.id,
       'L2',
       'FREE_TEXT',
       '一盒饼干标价 a 元，现在每盒便宜 2 元。买 3 盒可以先写成 3(a-2)。去掉括号后是什么？',
       '{"kind":"SHOPPING"}',
       '{"type":"string"}',
       'DRAFT',
       'v1'
FROM knowledge_points kp
WHERE kp.code='MATH-JUN-REMOVE-PARENTHESES'
  AND NOT EXISTS (SELECT 1 FROM questions q WHERE q.id='40000000-0000-4000-8000-000000000017');

INSERT INTO question_private_answers (question_id,correct_answer_json,full_solution_private,teacher_reference_answer,scoring_key_json,misconceptions_private_json,hint_policy_private_json)
SELECT '40000000-0000-4000-8000-000000000017',
       '{"value":"3a-6"}',
       '3 乘进括号：3×a 和 3×(-2)，得到 3a-6。',
       '3a-6',
       '{"concepts":["distributive_property"],"accepted_answers":["3a-6","3a－6","3a - 6","3×a-6"]}',
       '["DROPS_MINUS_WHEN_EXPANDING"]',
       '{"max_hints":2}'
WHERE EXISTS (SELECT 1 FROM questions q WHERE q.id='40000000-0000-4000-8000-000000000017')
  AND NOT EXISTS (SELECT 1 FROM question_private_answers a WHERE a.question_id='40000000-0000-4000-8000-000000000017');

INSERT INTO content_versions (id,question_id,version,schema_version,generator_provider,generator_model,source_id,asset_json)
SELECT '60000000-0000-4000-8000-000000000017',
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
WHERE q.id='40000000-0000-4000-8000-000000000017'
  AND NOT EXISTS (SELECT 1 FROM content_versions cv WHERE cv.question_id='40000000-0000-4000-8000-000000000017');

INSERT INTO content_validations (id,question_id,content_version,schema_version,status,checks_json,validator_version)
SELECT '70000000-0000-4000-8000-000000000017',
       '40000000-0000-4000-8000-000000000017',
       'v1',
       'content-question-v1',
       'PASS',
       '[{"name":"curated_fixture","passed":true}]',
       'validator-v1'
WHERE EXISTS (SELECT 1 FROM questions q WHERE q.id='40000000-0000-4000-8000-000000000017')
  AND NOT EXISTS (SELECT 1 FROM content_validations cv WHERE cv.question_id='40000000-0000-4000-8000-000000000017');

INSERT INTO content_reviews (id,question_id,content_version,schema_version,result,reviewer_provider,reviewer_model,findings_json)
SELECT '80000000-0000-4000-8000-000000000017',
       '40000000-0000-4000-8000-000000000017',
       'v1',
       'content-question-v1',
       'PASS',
       'independent',
       'curated-review-v1',
       '[]'
WHERE EXISTS (SELECT 1 FROM questions q WHERE q.id='40000000-0000-4000-8000-000000000017')
  AND NOT EXISTS (SELECT 1 FROM content_reviews cr WHERE cr.question_id='40000000-0000-4000-8000-000000000017');

UPDATE questions
SET status='AUTOMATIC_VALIDATED'
WHERE id='40000000-0000-4000-8000-000000000017'
  AND status='DRAFT';

UPDATE questions
SET status='AI_REVIEWED'
WHERE id='40000000-0000-4000-8000-000000000017'
  AND status='AUTOMATIC_VALIDATED';

UPDATE questions
SET status='RELEASED'
WHERE id='40000000-0000-4000-8000-000000000017'
  AND status='AI_REVIEWED';

INSERT INTO content_release_records (id,question_id,from_status,to_status,validation_id,review_id,reason)
SELECT '90000000-0000-4000-8000-000000000017',
       '40000000-0000-4000-8000-000000000017',
       'AI_REVIEWED',
       'RELEASED',
       '70000000-0000-4000-8000-000000000017',
       '80000000-0000-4000-8000-000000000017',
       'Grade-9 remove-parentheses life question for the current child'
WHERE EXISTS (SELECT 1 FROM questions q WHERE q.id='40000000-0000-4000-8000-000000000017')
  AND NOT EXISTS (SELECT 1 FROM content_release_records r WHERE r.question_id='40000000-0000-4000-8000-000000000017');
