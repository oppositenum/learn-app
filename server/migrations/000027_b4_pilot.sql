CREATE TABLE classroom_task_lineages (
    id uuid PRIMARY KEY,
    knowledge_point_id uuid NOT NULL REFERENCES knowledge_points(id),
    version text NOT NULL,
    status text NOT NULL CHECK (status IN ('PILOT_READY', 'RETIRED')),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (knowledge_point_id, version)
);

CREATE TABLE classroom_stage_tasks (
    question_id uuid PRIMARY KEY REFERENCES questions(id),
    lineage_id uuid NOT NULL REFERENCES classroom_task_lineages(id),
    stage_role text NOT NULL CHECK (stage_role IN ('ORIGINAL', 'VARIANT', 'ABSTRACT', 'VERIFY')),
    selection_order smallint NOT NULL CHECK (selection_order > 0),
    scoring_rule_version text NOT NULL,
    scoring_rule_private_json jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (lineage_id, stage_role, selection_order),
    UNIQUE (lineage_id, question_id)
);

REVOKE ALL ON classroom_stage_tasks FROM PUBLIC;

CREATE TABLE b4_pilot_sessions (
    session_id uuid PRIMARY KEY REFERENCES learning_sessions(id) ON DELETE CASCADE,
    lineage_id uuid NOT NULL REFERENCES classroom_task_lineages(id),
    knowledge_point_id uuid NOT NULL REFERENCES knowledge_points(id),
    current_task_id uuid NOT NULL REFERENCES classroom_stage_tasks(question_id),
    current_task_version text NOT NULL,
    requires_independent_reproof boolean NOT NULL DEFAULT false,
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    started_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz
);

CREATE TABLE b4_pilot_attempts (
    id uuid PRIMARY KEY,
    session_id uuid NOT NULL REFERENCES b4_pilot_sessions(session_id) ON DELETE CASCADE,
    operation_id uuid NOT NULL,
    request_digest text NOT NULL CHECK (request_digest ~ '^[0-9a-f]{64}$'),
    submitted_stage text NOT NULL CHECK (submitted_stage IN ('ORIGINAL', 'VARIANT', 'ABSTRACT', 'VERIFY')),
    question_id uuid NOT NULL REFERENCES classroom_stage_tasks(question_id),
    question_version text NOT NULL,
    attempt_kind text NOT NULL CHECK (attempt_kind IN ('ANSWER', 'HELP')),
    deterministic_result text NOT NULL CHECK (deterministic_result IN ('CORRECT', 'INCORRECT', 'INDETERMINATE', 'HELP_REQUESTED')),
    help_delivered boolean NOT NULL DEFAULT false,
    task_success boolean NOT NULL DEFAULT false,
    evidence_kind text NOT NULL CHECK (evidence_kind IN ('NONE', 'ASSISTED', 'INDEPENDENT')),
    stage_completed boolean NOT NULL DEFAULT false,
    model_feedback_used boolean NOT NULL DEFAULT false,
    response_code text NOT NULL CHECK (response_code IN ('TRY_AGAIN', 'HELP_DELIVERED', 'ASSISTED_REPROOF', 'NEXT_STAGE', 'PILOT_COMPLETE')),
    response_stage text NOT NULL CHECK (response_stage IN ('ORIGINAL', 'VARIANT', 'ABSTRACT', 'VERIFY', 'COMPLETE')),
    response_task_id uuid REFERENCES classroom_stage_tasks(question_id),
    response_task_version text,
    session_completed boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (session_id, operation_id),
    CHECK (task_success = (evidence_kind <> 'NONE')),
    CHECK (NOT stage_completed OR (task_success AND evidence_kind = 'INDEPENDENT')),
    CHECK (help_delivered = (attempt_kind = 'HELP' AND model_feedback_used)),
    CHECK ((response_stage = 'COMPLETE' AND response_task_id IS NULL AND response_task_version IS NULL AND session_completed)
        OR (response_stage <> 'COMPLETE' AND response_task_id IS NOT NULL AND response_task_version IS NOT NULL AND NOT session_completed))
);

CREATE UNIQUE INDEX b4_pilot_one_task_success
ON b4_pilot_attempts (session_id, submitted_stage, question_id)
WHERE task_success;

CREATE UNIQUE INDEX b4_pilot_one_stage_completion
ON b4_pilot_attempts (session_id, submitted_stage)
WHERE stage_completed;

INSERT INTO classroom_task_lineages (id, knowledge_point_id, version, status) VALUES
('42000000-0000-4000-8000-000000000001', '30000000-0000-4000-8000-000000000004', 'b4-pilot-v1', 'PILOT_READY');

INSERT INTO questions
(id, knowledge_point_id, difficulty, question_type, prompt_public, scene_public_json, input_schema_json, status, content_version)
VALUES
('41000000-0000-4000-8000-000000000001', '30000000-0000-4000-8000-000000000004', 'L1', 'STRUCTURED_SELECTION',
 '阅读社区活动通知，选出问题要求的两个明确信息项。',
 '{"kind":"NOTICE","options":[{"id":"orig-a","text":"本周六上午九点"},{"id":"orig-b","text":"七年级志愿者"},{"id":"orig-c","text":"文化中心东门"},{"id":"orig-d","text":"自带遮阳帽"}]}',
 '{"type":"object","properties":{"selected_option_ids":{"type":"array","items":{"type":"string"}}},"required":["selected_option_ids"],"additionalProperties":false}',
 'DRAFT', 'b4-pilot-v1');

INSERT INTO question_private_answers
(question_id, correct_answer_json, full_solution_private, teacher_reference_answer, scoring_key_json, misconceptions_private_json, hint_policy_private_json)
VALUES
('41000000-0000-4000-8000-000000000001', '{"selected_option_ids":["orig-a","orig-c"]}',
 '先确定题目要求的是时间和地点，再分别定位通知中的对应信息。',
 '选择通知中明确给出的时间项和地点项。',
 '{"rule_type":"EXACT_OPTION_SET","expected_option_ids":["orig-a","orig-c"],"allowed_option_ids":["orig-a","orig-b","orig-c","orig-d"]}',
 '[]', '{"max_hints":2}');

INSERT INTO questions
(id, knowledge_point_id, difficulty, question_type, prompt_public, scene_public_json, input_schema_json, status, content_version)
VALUES
('41000000-0000-4000-8000-000000000002', '30000000-0000-4000-8000-000000000004', 'L1', 'STRUCTURED_SELECTION',
 '阅读图书馆临时开放通知，选出其中明确给出的两个时间信息。',
 '{"kind":"NOTICE","options":[{"id":"var1-a","text":"周三下午四点闭馆"},{"id":"var1-b","text":"少儿阅览室"},{"id":"var1-c","text":"设备检修"},{"id":"var1-d","text":"周四上午十点恢复开放"}]}',
 '{"type":"object","properties":{"selected_option_ids":{"type":"array","items":{"type":"string"}}},"required":["selected_option_ids"],"additionalProperties":false}',
 'DRAFT', 'b4-pilot-v1'),
('41000000-0000-4000-8000-000000000003', '30000000-0000-4000-8000-000000000004', 'L1', 'STRUCTURED_SELECTION',
 '阅读校车调整通知，选出其中明确给出的适用对象和调整内容。',
 '{"kind":"NOTICE","options":[{"id":"var2-a","text":"从下周一开始"},{"id":"var2-b","text":"二号校车"},{"id":"var2-c","text":"提前十分钟发车"},{"id":"var2-d","text":"雨天道路拥堵"}]}',
 '{"type":"object","properties":{"selected_option_ids":{"type":"array","items":{"type":"string"}}},"required":["selected_option_ids"],"additionalProperties":false}',
 'DRAFT', 'b4-pilot-v1');

INSERT INTO question_private_answers
(question_id, correct_answer_json, full_solution_private, teacher_reference_answer, scoring_key_json, misconceptions_private_json, hint_policy_private_json)
VALUES
('41000000-0000-4000-8000-000000000002', '{"selected_option_ids":["var1-a","var1-d"]}',
 '先锁定题目要求的时间信息，再从通知中排除地点和原因。',
 '选择两处明确的时间信息。',
 '{"rule_type":"EXACT_OPTION_SET","expected_option_ids":["var1-a","var1-d"],"allowed_option_ids":["var1-a","var1-b","var1-c","var1-d"]}',
 '[]', '{"max_hints":2}'),
('41000000-0000-4000-8000-000000000003', '{"selected_option_ids":["var2-b","var2-c"]}',
 '先区分生效时间、适用对象、调整内容和背景，再按问题提取对应两项。',
 '选择适用对象和调整内容。',
 '{"rule_type":"EXACT_OPTION_SET","expected_option_ids":["var2-b","var2-c"],"allowed_option_ids":["var2-a","var2-b","var2-c","var2-d"]}',
 '[]', '{"max_hints":2}');

INSERT INTO questions
(id, knowledge_point_id, difficulty, question_type, prompt_public, scene_public_json, input_schema_json, status, content_version)
VALUES
('41000000-0000-4000-8000-000000000004', '30000000-0000-4000-8000-000000000004', 'L2', 'STRUCTURED_SELECTION',
 '从四种做法中，选出提取明确信息时都适用的两个步骤。',
 '{"kind":"METHOD","options":[{"id":"abs1-a","text":"先确认问题要求的信息类别"},{"id":"abs1-b","text":"把材料中的每句话都抄下来"},{"id":"abs1-c","text":"逐项核对所选内容是否由材料明确给出"},{"id":"abs1-d","text":"补写材料没有说明的细节"}]}',
 '{"type":"object","properties":{"selected_option_ids":{"type":"array","items":{"type":"string"}}},"required":["selected_option_ids"],"additionalProperties":false}',
 'DRAFT', 'b4-pilot-v1'),
('41000000-0000-4000-8000-000000000005', '30000000-0000-4000-8000-000000000004', 'L2', 'STRUCTURED_SELECTION',
 '从四条规则中，选出能减少信息提取遗漏或混淆的两条通用规则。',
 '{"kind":"METHOD","options":[{"id":"abs2-a","text":"按问题列出需要寻找的字段"},{"id":"abs2-b","text":"只挑自己觉得重要的信息"},{"id":"abs2-c","text":"将每个字段与材料中的明确表述对应"},{"id":"abs2-d","text":"遇到空缺时根据经验补全"}]}',
 '{"type":"object","properties":{"selected_option_ids":{"type":"array","items":{"type":"string"}}},"required":["selected_option_ids"],"additionalProperties":false}',
 'DRAFT', 'b4-pilot-v1');

INSERT INTO question_private_answers
(question_id, correct_answer_json, full_solution_private, teacher_reference_answer, scoring_key_json, misconceptions_private_json, hint_policy_private_json)
VALUES
('41000000-0000-4000-8000-000000000004', '{"selected_option_ids":["abs1-a","abs1-c"]}',
 '可靠提取先限定目标字段，再以材料中的明确表述逐项核对；整段抄写和补写未知细节都不满足要求。',
 '选择限定字段和逐项核对两种做法。',
 '{"rule_type":"EXACT_OPTION_SET","expected_option_ids":["abs1-a","abs1-c"],"allowed_option_ids":["abs1-a","abs1-b","abs1-c","abs1-d"]}',
 '[]', '{"max_hints":2}'),
('41000000-0000-4000-8000-000000000005', '{"selected_option_ids":["abs2-a","abs2-c"]}',
 '先把问题拆成字段，再把字段与材料中的明确表述对应，可以系统地减少遗漏和类别混淆。',
 '选择按字段寻找并与原材料对应的规则。',
 '{"rule_type":"EXACT_OPTION_SET","expected_option_ids":["abs2-a","abs2-c"],"allowed_option_ids":["abs2-a","abs2-b","abs2-c","abs2-d"]}',
 '[]', '{"max_hints":2}');

INSERT INTO questions
(id, knowledge_point_id, difficulty, question_type, prompt_public, scene_public_json, input_schema_json, status, content_version)
VALUES
('41000000-0000-4000-8000-000000000006', '30000000-0000-4000-8000-000000000004', 'L2', 'STRUCTURED_SELECTION',
 '独立阅读比赛安排，选出其中明确给出的报名截止时间和报到地点。',
 '{"kind":"NOTICE","options":[{"id":"ver1-a","text":"本月十八日下午五点"},{"id":"ver1-b","text":"科技创意比赛"},{"id":"ver1-c","text":"实验楼一层大厅"},{"id":"ver1-d","text":"每组三名同学"}]}',
 '{"type":"object","properties":{"selected_option_ids":{"type":"array","items":{"type":"string"}}},"required":["selected_option_ids"],"additionalProperties":false}',
 'DRAFT', 'b4-pilot-v1'),
('41000000-0000-4000-8000-000000000007', '30000000-0000-4000-8000-000000000004', 'L2', 'STRUCTURED_SELECTION',
 '独立阅读场馆公告，选出其中明确给出的暂停开放区域和恢复开放时间。',
 '{"kind":"NOTICE","options":[{"id":"ver2-a","text":"二层展厅"},{"id":"ver2-b","text":"进行设备维护"},{"id":"ver2-c","text":"周日上午九点"},{"id":"ver2-d","text":"从南门进入"}]}',
 '{"type":"object","properties":{"selected_option_ids":{"type":"array","items":{"type":"string"}}},"required":["selected_option_ids"],"additionalProperties":false}',
 'DRAFT', 'b4-pilot-v1');

INSERT INTO question_private_answers
(question_id, correct_answer_json, full_solution_private, teacher_reference_answer, scoring_key_json, misconceptions_private_json, hint_policy_private_json)
VALUES
('41000000-0000-4000-8000-000000000006', '{"selected_option_ids":["ver1-a","ver1-c"]}',
 '按题目要求分别锁定截止时间与报到地点，再从公告中逐项定位。',
 '选择报名截止时间和报到地点。',
 '{"rule_type":"EXACT_OPTION_SET","expected_option_ids":["ver1-a","ver1-c"],"allowed_option_ids":["ver1-a","ver1-b","ver1-c","ver1-d"]}',
 '[]', '{"max_hints":2}'),
('41000000-0000-4000-8000-000000000007', '{"selected_option_ids":["ver2-a","ver2-c"]}',
 '按题目要求分别锁定区域与时间，原因和入口信息不是本题所需字段。',
 '选择暂停区域和恢复时间。',
 '{"rule_type":"EXACT_OPTION_SET","expected_option_ids":["ver2-a","ver2-c"],"allowed_option_ids":["ver2-a","ver2-b","ver2-c","ver2-d"]}',
 '[]', '{"max_hints":2}');

INSERT INTO content_versions
(id, question_id, version, schema_version, generator_provider, generator_model, generator_request_id, source_id, asset_json)
SELECT
    ('43' || substr(replace(question.id::text, '-', ''), 3))::uuid,
    question.id,
    question.content_version,
    'content-question-v1',
    'internal',
    'b4-pilot-curation-v1',
    'b4-pilot-content-authoring',
    '50000000-0000-4000-8000-000000000001',
    jsonb_build_object(
        'question_id', question.id,
        'knowledge_point_id', question.knowledge_point_id,
        'question_type', question.question_type,
        'prompt_public', question.prompt_public,
        'scene_public_json', question.scene_public_json,
        'input_schema_json', question.input_schema_json,
        'content_version', question.content_version,
        'status', 'DRAFT'
    )
FROM questions question
WHERE question.id::text LIKE '41000000-0000-4000-8000-00000000000_';

INSERT INTO content_validations
(id, question_id, content_version, schema_version, status, checks_json, validator_version)
SELECT
    ('44' || substr(replace(question.id::text, '-', ''), 3))::uuid,
    question.id,
    question.content_version,
    'content-question-v1',
    'PASS',
    '[{"name":"schema","passed":true},{"name":"answer_not_public","passed":true},{"name":"choices","passed":true},{"name":"solution","passed":true},{"name":"duplicate","passed":true}]',
    'validator-v1'
FROM questions question
WHERE question.id::text LIKE '41000000-0000-4000-8000-00000000000_';

UPDATE questions
SET status = 'AUTOMATIC_VALIDATED'
WHERE id::text LIKE '41000000-0000-4000-8000-00000000000_';

INSERT INTO content_reviews
(id, question_id, content_version, schema_version, result, reviewer_provider, reviewer_model, reviewer_request_id, findings_json)
SELECT
    ('45' || substr(replace(question.id::text, '-', ''), 3))::uuid,
    question.id,
    question.content_version,
    'content-question-v1',
    'PASS',
    'independent',
    'curated-review-v1',
    'b4-pilot-independent-review-' || right(question.id::text, 1),
    '[]'
FROM questions question
WHERE question.id::text LIKE '41000000-0000-4000-8000-00000000000_';

UPDATE questions
SET status = 'AI_REVIEWED'
WHERE id::text LIKE '41000000-0000-4000-8000-00000000000_';

UPDATE questions
SET status = 'RELEASED'
WHERE id::text LIKE '41000000-0000-4000-8000-00000000000_';

INSERT INTO content_release_records
(id, question_id, from_status, to_status, validation_id, review_id, reason)
SELECT
    ('46' || substr(replace(question.id::text, '-', ''), 3))::uuid,
    question.id,
    'AI_REVIEWED',
    'RELEASED',
    ('44' || substr(replace(question.id::text, '-', ''), 3))::uuid,
    ('45' || substr(replace(question.id::text, '-', ''), 3))::uuid,
    'B4 pilot curated benchmark release'
FROM questions question
WHERE question.id::text LIKE '41000000-0000-4000-8000-00000000000_';

INSERT INTO classroom_stage_tasks
(question_id, lineage_id, stage_role, selection_order, scoring_rule_version, scoring_rule_private_json)
SELECT
    answer.question_id,
    '42000000-0000-4000-8000-000000000001',
    role.stage_role,
    role.selection_order,
    'exact-option-set-v1',
    answer.scoring_key_json
FROM question_private_answers answer
JOIN (VALUES
    ('41000000-0000-4000-8000-000000000001'::uuid, 'ORIGINAL', 1),
    ('41000000-0000-4000-8000-000000000002'::uuid, 'VARIANT', 1),
    ('41000000-0000-4000-8000-000000000003'::uuid, 'VARIANT', 2),
    ('41000000-0000-4000-8000-000000000004'::uuid, 'ABSTRACT', 1),
    ('41000000-0000-4000-8000-000000000005'::uuid, 'ABSTRACT', 2),
    ('41000000-0000-4000-8000-000000000006'::uuid, 'VERIFY', 1),
    ('41000000-0000-4000-8000-000000000007'::uuid, 'VERIFY', 2)
) AS role(question_id, stage_role, selection_order)
ON role.question_id = answer.question_id;
