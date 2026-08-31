INSERT INTO domains (id, subject_id, code, name, description, sort_order) VALUES
('10000000-0000-4000-8000-000000000001','00000000-0000-4000-8000-000000000001','DEMO_NUMBER_REASONING','数与代数推理','从生活数量关系回到课本表达',1),
('10000000-0000-4000-8000-000000000002','00000000-0000-4000-8000-000000000002','DEMO_READING','阅读与证据','提取信息并用文本证据解释',1),
('10000000-0000-4000-8000-000000000003','00000000-0000-4000-8000-000000000003','DEMO_LANGUAGE','语言运用','在真实语境中理解语法与阅读',1),
('10000000-0000-4000-8000-000000000004','00000000-0000-4000-8000-000000000004','DEMO_MECHANICS','运动与力','用数量和证据解释运动现象',1),
('10000000-0000-4000-8000-000000000005','00000000-0000-4000-8000-000000000005','DEMO_MATTER','物质世界','从观察区分变化、组成与元素',1);

INSERT INTO units (id, domain_id, code, name, grade_band_code, sort_order) VALUES
('20000000-0000-4000-8000-000000000001','10000000-0000-4000-8000-000000000001','DEMO_MATH','数量关系','JUNIOR_SECONDARY',1),
('20000000-0000-4000-8000-000000000002','10000000-0000-4000-8000-000000000002','DEMO_CHINESE','阅读证据','JUNIOR_SECONDARY',1),
('20000000-0000-4000-8000-000000000003','10000000-0000-4000-8000-000000000003','DEMO_ENGLISH','语法与阅读','JUNIOR_SECONDARY',1),
('20000000-0000-4000-8000-000000000004','10000000-0000-4000-8000-000000000004','DEMO_PHYSICS','机械运动','JUNIOR_SECONDARY',1),
('20000000-0000-4000-8000-000000000005','10000000-0000-4000-8000-000000000005','DEMO_CHEMISTRY','物质构成与变化','JUNIOR_SECONDARY',1);

INSERT INTO knowledge_points
(id,subject_id,domain_id,unit_id,grade_band_code,code,name,description,why_it_matters_json,default_difficulty,status,curriculum_version) VALUES
('30000000-0000-4000-8000-000000000001','00000000-0000-4000-8000-000000000001','10000000-0000-4000-8000-000000000001','20000000-0000-4000-8000-000000000001','JUNIOR_SECONDARY','MATH-LINEAR-EQUATION','一元一次方程','从总量与固定量建立未知数关系','{"daily_life":"分摊费用","human_world":"比较计费方案","future_learning":"函数","career_or_science":"建模"}','L2','RELEASED','v1'),
('30000000-0000-4000-8000-000000000002','00000000-0000-4000-8000-000000000001','10000000-0000-4000-8000-000000000001','20000000-0000-4000-8000-000000000001','JUNIOR_SECONDARY','MATH-FRACTION-COMMON-DENOMINATOR','分数通分','用共同单位比较和合并分数','{"daily_life":"配方","human_world":"数据比较","future_learning":"分式","career_or_science":"比例计算"}','L2','RELEASED','v1'),
('30000000-0000-4000-8000-000000000003','00000000-0000-4000-8000-000000000001','10000000-0000-4000-8000-000000000001','20000000-0000-4000-8000-000000000001','JUNIOR_SECONDARY','MATH-PERCENT-DISCOUNT','百分数与折扣','区分折扣比例与减少的绝对量','{"daily_life":"购物折扣","human_world":"统计变化","future_learning":"增长率","career_or_science":"财务分析"}','L2','RELEASED','v1'),
('30000000-0000-4000-8000-000000000004','00000000-0000-4000-8000-000000000002','10000000-0000-4000-8000-000000000002','20000000-0000-4000-8000-000000000002','JUNIOR_SECONDARY','CHINESE-INFORMATION-EXTRACTION','信息提取','区分人物、事件、时间和条件','{"daily_life":"读通知","human_world":"理解规则","future_learning":"说明文阅读","career_or_science":"需求分析"}','L1','RELEASED','v1'),
('30000000-0000-4000-8000-000000000005','00000000-0000-4000-8000-000000000002','10000000-0000-4000-8000-000000000002','20000000-0000-4000-8000-000000000002','JUNIOR_SECONDARY','CHINESE-EVIDENCE-LOCATION','证据定位','用原文证据支撑判断','{"daily_life":"澄清误会","human_world":"新闻证据","future_learning":"议论文阅读","career_or_science":"论证"}','L2','RELEASED','v1'),
('30000000-0000-4000-8000-000000000006','00000000-0000-4000-8000-000000000002','10000000-0000-4000-8000-000000000002','20000000-0000-4000-8000-000000000002','JUNIOR_SECONDARY','CHINESE-POETRY-IMAGERY','古诗意象理解','从景物与动作推断情感','{"daily_life":"感受表达","human_world":"文化记忆","future_learning":"诗歌鉴赏","career_or_science":"人文理解"}','L2','RELEASED','v1'),
('30000000-0000-4000-8000-000000000007','00000000-0000-4000-8000-000000000003','10000000-0000-4000-8000-000000000003','20000000-0000-4000-8000-000000000003','JUNIOR_SECONDARY','ENGLISH-THERE-IS-ARE','There is/are','根据后接名词选择单复数','{"daily_life":"描述房间","human_world":"介绍城市","future_learning":"主谓一致","career_or_science":"客观描述"}','L1','RELEASED','v1'),
('30000000-0000-4000-8000-000000000008','00000000-0000-4000-8000-000000000003','10000000-0000-4000-8000-000000000003','20000000-0000-4000-8000-000000000003','JUNIOR_SECONDARY','ENGLISH-SIMPLE-PRESENT','一般现在时','表达习惯、规律和事实','{"daily_life":"介绍作息","human_world":"描述习俗","future_learning":"时态体系","career_or_science":"陈述规律"}','L2','RELEASED','v1'),
('30000000-0000-4000-8000-000000000009','00000000-0000-4000-8000-000000000003','10000000-0000-4000-8000-000000000003','20000000-0000-4000-8000-000000000003','JUNIOR_SECONDARY','ENGLISH-READING-DETAIL','英语阅读细节','根据关键词定位明确事实','{"daily_life":"读英文告示","human_world":"获取国际信息","future_learning":"篇章阅读","career_or_science":"资料检索"}','L2','RELEASED','v1'),
('30000000-0000-4000-8000-000000000010','00000000-0000-4000-8000-000000000004','10000000-0000-4000-8000-000000000004','20000000-0000-4000-8000-000000000004','JUNIOR_SECONDARY','PHYSICS-SPEED','速度','用路程与时间描述运动快慢','{"daily_life":"规划出行","human_world":"交通安全","future_learning":"运动图像","career_or_science":"工程测量"}','L2','RELEASED','v1'),
('30000000-0000-4000-8000-000000000011','00000000-0000-4000-8000-000000000004','10000000-0000-4000-8000-000000000004','20000000-0000-4000-8000-000000000004','JUNIOR_SECONDARY','PHYSICS-DENSITY','密度','用质量与体积辨别物质特征','{"daily_life":"选材料","human_world":"资源运输","future_learning":"浮力","career_or_science":"材料科学"}','L2','RELEASED','v1'),
('30000000-0000-4000-8000-000000000012','00000000-0000-4000-8000-000000000004','10000000-0000-4000-8000-000000000004','20000000-0000-4000-8000-000000000004','JUNIOR_SECONDARY','PHYSICS-FORCE','力与作用效果','从形变和运动状态变化识别力的效果','{"daily_life":"安全用力","human_world":"体育运动","future_learning":"牛顿运动规律","career_or_science":"机械设计"}','L1','RELEASED','v1'),
('30000000-0000-4000-8000-000000000013','00000000-0000-4000-8000-000000000005','10000000-0000-4000-8000-000000000005','20000000-0000-4000-8000-000000000005','JUNIOR_SECONDARY','CHEMISTRY-CHANGE-TYPES','物理变化与化学变化','依据是否生成新物质区分变化','{"daily_life":"烹饪观察","human_world":"材料加工","future_learning":"化学反应","career_or_science":"实验判断"}','L1','RELEASED','v1'),
('30000000-0000-4000-8000-000000000014','00000000-0000-4000-8000-000000000005','10000000-0000-4000-8000-000000000005','20000000-0000-4000-8000-000000000005','JUNIOR_SECONDARY','CHEMISTRY-AIR-OXYGEN','空气与氧气','用实验现象理解空气的组成','{"daily_life":"燃烧安全","human_world":"空气质量","future_learning":"气体反应","career_or_science":"环境监测"}','L2','RELEASED','v1'),
('30000000-0000-4000-8000-000000000015','00000000-0000-4000-8000-000000000005','10000000-0000-4000-8000-000000000005','20000000-0000-4000-8000-000000000005','JUNIOR_SECONDARY','CHEMISTRY-ELEMENT-SYMBOL','元素与元素符号','区分元素名称、符号与物质','{"daily_life":"读营养标签","human_world":"统一科学语言","future_learning":"化学式","career_or_science":"实验记录"}','L1','RELEASED','v1');

INSERT INTO cross_subject_dependencies (id,source_knowledge_point_id,target_knowledge_point_id,relation,strength,failure_signal_json,remediation_policy_json) VALUES
('a0000000-0000-4000-8000-000000000001','30000000-0000-4000-8000-000000000010','30000000-0000-4000-8000-000000000002','REQUIRES',0.8,'["division_or_ratio_error"]','{"return_to_original":true,"max_minutes":4}'),
('a0000000-0000-4000-8000-000000000002','30000000-0000-4000-8000-000000000011','30000000-0000-4000-8000-000000000002','REQUIRES',0.8,'["mass_volume_ratio_error"]','{"return_to_original":true,"max_minutes":4}'),
('a0000000-0000-4000-8000-000000000003','30000000-0000-4000-8000-000000000014','30000000-0000-4000-8000-000000000005','SUPPORTED_BY',0.6,'["cannot_locate_experiment_evidence"]','{"return_to_original":true,"max_minutes":4}'),
('a0000000-0000-4000-8000-000000000004','30000000-0000-4000-8000-000000000009','30000000-0000-4000-8000-000000000004','SUPPORTED_BY',0.7,'["misses_explicit_detail"]','{"return_to_original":true,"max_minutes":4}');

CREATE TABLE scenarios (
    id uuid PRIMARY KEY,
    kind text NOT NULL,
    title text NOT NULL,
    context_public_json jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE world_connections (
    id uuid PRIMARY KEY,
    knowledge_point_id uuid NOT NULL REFERENCES knowledge_points(id) ON DELETE CASCADE,
    connection_type text NOT NULL CHECK (connection_type IN ('DAILY_LIFE','HUMAN_WORLD','SCIENCE_OR_CAREER')),
    title text NOT NULL,
    explanation text NOT NULL,
    UNIQUE (knowledge_point_id, connection_type)
);

INSERT INTO world_connections (id,knowledge_point_id,connection_type,title,explanation)
SELECT ('b' || substr(replace(k.id::text,'-',''),2))::uuid, k.id, 'DAILY_LIFE', k.name || '在生活中', k.why_it_matters_json->>'daily_life' FROM knowledge_points k WHERE k.curriculum_version='v1' AND k.id::text LIKE '30000000-%';
INSERT INTO world_connections (id,knowledge_point_id,connection_type,title,explanation)
SELECT ('c' || substr(replace(k.id::text,'-',''),2))::uuid, k.id, 'HUMAN_WORLD', k.name || '与世界', k.why_it_matters_json->>'human_world' FROM knowledge_points k WHERE k.curriculum_version='v1' AND k.id::text LIKE '30000000-%';
INSERT INTO world_connections (id,knowledge_point_id,connection_type,title,explanation)
SELECT ('d' || substr(replace(k.id::text,'-',''),2))::uuid, k.id, 'SCIENCE_OR_CAREER', k.name || '与未来', k.why_it_matters_json->>'career_or_science' FROM knowledge_points k WHERE k.curriculum_version='v1' AND k.id::text LIKE '30000000-%';

INSERT INTO content_sources (id,name,source_type,license_code,attribution) VALUES
('50000000-0000-4000-8000-000000000001','V1 原创示范课程','INTERNAL_RULE','INTERNAL-ORIGINAL','AI Learning Tutor V1 engineering prototype');

INSERT INTO questions (id,knowledge_point_id,difficulty,question_type,prompt_public,scene_public_json,input_schema_json,status,content_version) VALUES
('40000000-0000-4000-8000-000000000001','30000000-0000-4000-8000-000000000001','L2','FREE_TEXT','三张同价门票加6元服务费共36元。你会先怎样表示每张门票的价格？','{"kind":"MONEY"}','{"type":"string"}','DRAFT','v1'),
('40000000-0000-4000-8000-000000000002','30000000-0000-4000-8000-000000000002','L2','FREE_TEXT','一杯果汁用了三分之一杯橙汁和四分之一杯苹果汁。怎样把两部分放到同一种小格里比较？','{"kind":"FOOD"}','{"type":"string"}','DRAFT','v1'),
('40000000-0000-4000-8000-000000000003','30000000-0000-4000-8000-000000000003','L2','FREE_TEXT','一件80元的物品打八折。这里的八折表示保留原价的多少？','{"kind":"SHOPPING"}','{"type":"string"}','DRAFT','v1'),
('40000000-0000-4000-8000-000000000004','30000000-0000-4000-8000-000000000004','L1','FREE_TEXT','通知写着：周五下午三点，七年级志愿者在图书馆门口集合。请提取时间和地点。','{"kind":"CITY"}','{"type":"string"}','DRAFT','v1'),
('40000000-0000-4000-8000-000000000005','30000000-0000-4000-8000-000000000005','L2','FREE_TEXT','短文说“小林把伞递给没带雨具的同学”。哪处文字能支持小林乐于助人的判断？','{"kind":"HUMAN_BEHAVIOR"}','{"type":"string"}','DRAFT','v1'),
('40000000-0000-4000-8000-000000000006','30000000-0000-4000-8000-000000000006','L2','FREE_TEXT','“月落乌啼霜满天，江枫渔火对愁眠”中，哪些景物共同营造了清冷的夜景？','{"kind":"ART"}','{"type":"string"}','DRAFT','v1'),
('40000000-0000-4000-8000-000000000007','30000000-0000-4000-8000-000000000007','L1','FREE_TEXT','Look at one map and two notebooks on the desk. Complete: There ___ one map and there ___ two notebooks.','{"kind":"HOME"}','{"type":"string"}','DRAFT','v1'),
('40000000-0000-4000-8000-000000000008','30000000-0000-4000-8000-000000000008','L2','FREE_TEXT','Ming reads for twenty minutes every evening. Why do we use “reads” instead of “is reading” to describe this sentence?','{"kind":"HOME"}','{"type":"string"}','DRAFT','v1'),
('40000000-0000-4000-8000-000000000009','30000000-0000-4000-8000-000000000009','L2','FREE_TEXT','The museum opens at 9:00 and the science show begins at 10:30. What time does the science show begin?','{"kind":"SCIENCE"}','{"type":"string"}','DRAFT','v1'),
('40000000-0000-4000-8000-000000000010','30000000-0000-4000-8000-000000000010','L2','FREE_TEXT','自行车在2小时内行驶30千米。要比较它每小时行多远，你会用哪两个量？','{"kind":"TRANSPORT"}','{"type":"string"}','DRAFT','v1'),
('40000000-0000-4000-8000-000000000011','30000000-0000-4000-8000-000000000011','L2','FREE_TEXT','两个体积相同的金属块质量不同。要比较它们的密度，还需要怎样使用质量和体积？','{"kind":"SCIENCE"}','{"type":"string"}','DRAFT','v1'),
('40000000-0000-4000-8000-000000000012','30000000-0000-4000-8000-000000000012','L1','FREE_TEXT','足球被踢后由静止变为运动。这个现象说明力可以改变物体的什么？','{"kind":"SPORT"}','{"type":"string"}','DRAFT','v1'),
('40000000-0000-4000-8000-000000000013','30000000-0000-4000-8000-000000000013','L1','FREE_TEXT','冰融化成水和铁钉生锈，哪一个过程生成了新物质？你依据什么判断？','{"kind":"HOME"}','{"type":"string"}','DRAFT','v1'),
('40000000-0000-4000-8000-000000000014','30000000-0000-4000-8000-000000000014','L2','FREE_TEXT','燃烧的红磷消耗密闭容器中的氧气。冷却后液面上升，这个现象能帮助说明空气中的什么？','{"kind":"SCIENCE"}','{"type":"string"}','DRAFT','v1'),
('40000000-0000-4000-8000-000000000015','30000000-0000-4000-8000-000000000015','L1','FREE_TEXT','营养标签中写着 Na。这个符号表示一种元素、一个具体物体，还是一份混合物？','{"kind":"FOOD"}','{"type":"string"}','DRAFT','v1');

INSERT INTO question_private_answers (question_id,correct_answer_json,full_solution_private,teacher_reference_answer,scoring_key_json,misconceptions_private_json,hint_policy_private_json) VALUES
('40000000-0000-4000-8000-000000000001','{"value":"设每张x元，3x+6=36"}','先用未知数表示单价，再保留固定服务费建立等量关系。','设每张x元，3x+6=36','{"concepts":["unknown","fixed_cost"]}','["FIXED_COST_IGNORED"]','{"max_hints":2}'),
('40000000-0000-4000-8000-000000000002','{"value":"公分母12"}','把三分之一改写成十二分之四，把四分之一改写成十二分之三。','公分母可以取12','{"concepts":["equivalent_fraction"]}','["ADD_DENOMINATORS_DIRECTLY"]','{"max_hints":2}'),
('40000000-0000-4000-8000-000000000003','{"value":"80%"}','八折是现价占原价的百分之八十，而不是减少八元。','保留原价的80%','{"concepts":["percent_as_ratio"]}','["DISCOUNT_AS_ABSOLUTE"]','{"max_hints":2}'),
('40000000-0000-4000-8000-000000000004','{"value":"周五下午三点；图书馆门口"}','按问题只提取通知里的时间和地点。','时间是周五下午三点，地点是图书馆门口','{"concepts":["explicit_information"]}','["OMITS_CONDITION"]','{"max_hints":2}'),
('40000000-0000-4000-8000-000000000005','{"value":"把伞递给没带雨具的同学"}','判断必须回到原文动作证据。','把伞递给没带雨具的同学','{"concepts":["textual_evidence"]}','["OPINION_WITHOUT_EVIDENCE"]','{"max_hints":2}'),
('40000000-0000-4000-8000-000000000006','{"value":"月落、乌啼、霜天、江枫、渔火"}','列出诗句中的景物，再说明清冷氛围。','月落、乌啼、霜天、江枫和渔火','{"concepts":["imagery"]}','["PARAPHRASE_WITHOUT_IMAGE"]','{"max_hints":2}'),
('40000000-0000-4000-8000-000000000007','{"value":"is; are"}','one map 是单数，two notebooks 是复数。','is; are','{"concepts":["number_agreement"]}','["THERE_AGREEMENT"]','{"max_hints":2}'),
('40000000-0000-4000-8000-000000000008','{"value":"它描述每天重复的习惯"}','every evening 是规律性时间标记，一般现在时表达习惯。','因为句子描述每天重复的习惯','{"concepts":["habitual_action"]}','["TENSE_BY_CURRENT_TIME"]','{"max_hints":2}'),
('40000000-0000-4000-8000-000000000009','{"value":"10:30"}','直接定位 science show begins 后面的时间。','10:30','{"concepts":["detail_location"]}','["CONFUSES_OPENING_TIME"]','{"max_hints":2}'),
('40000000-0000-4000-8000-000000000010','{"value":"路程和时间"}','速度由路程除以时间得到，这里先识别30千米和2小时。','路程30千米和时间2小时','{"concepts":["distance_time"]}','["MULTIPLIES_DISTANCE_TIME"]','{"max_hints":2}'),
('40000000-0000-4000-8000-000000000011','{"value":"用质量除以体积"}','密度是单位体积的质量。','质量除以体积','{"concepts":["mass_per_volume"]}','["COMPARES_MASS_ONLY"]','{"max_hints":2}'),
('40000000-0000-4000-8000-000000000012','{"value":"运动状态"}','足球从静止变为运动，运动状态发生变化。','力可以改变物体的运动状态','{"concepts":["force_effect"]}','["FORCE_ONLY_DEFORMS"]','{"max_hints":2}'),
('40000000-0000-4000-8000-000000000013','{"value":"铁钉生锈；生成了新物质"}','铁锈与原来的铁性质不同；冰融化只是状态改变。','铁钉生锈，因为生成了新物质','{"concepts":["new_substance"]}','["STATE_CHANGE_IS_CHEMICAL"]','{"max_hints":2}'),
('40000000-0000-4000-8000-000000000014','{"value":"氧气约占空气体积的五分之一"}','红磷消耗氧气，液面上升的体积反映氧气所占比例。','氧气约占空气体积的五分之一','{"concepts":["experimental_inference"]}','["AIR_IS_ONLY_OXYGEN"]','{"max_hints":2}'),
('40000000-0000-4000-8000-000000000015','{"value":"钠元素"}','Na 是钠元素的国际通用元素符号。','Na 表示钠元素','{"concepts":["element_symbol"]}','["SYMBOL_IS_SUBSTANCE_SAMPLE"]','{"max_hints":2}');

INSERT INTO content_versions (id,question_id,version,schema_version,generator_provider,generator_model,source_id,asset_json)
SELECT ('6' || substr(replace(q.id::text,'-',''),2))::uuid,q.id,'v1','content-question-v1','internal','curated-v1','50000000-0000-4000-8000-000000000001',jsonb_build_object('question_id',q.id,'status','DRAFT') FROM questions q WHERE q.id::text LIKE '40000000-%';
INSERT INTO content_validations (id,question_id,content_version,schema_version,status,checks_json,validator_version)
SELECT ('7' || substr(replace(q.id::text,'-',''),2))::uuid,q.id,'v1','content-question-v1','PASS','[{"name":"curated_fixture","passed":true}]','validator-v1' FROM questions q WHERE q.id::text LIKE '40000000-%';
UPDATE questions SET status='AUTOMATIC_VALIDATED' WHERE id::text LIKE '40000000-%';
INSERT INTO content_reviews (id,question_id,content_version,schema_version,result,reviewer_provider,reviewer_model,findings_json)
SELECT ('8' || substr(replace(q.id::text,'-',''),2))::uuid,q.id,'v1','content-question-v1','PASS','independent','curated-review-v1','[]' FROM questions q WHERE q.id::text LIKE '40000000-%';
UPDATE questions SET status='AI_REVIEWED' WHERE id::text LIKE '40000000-%';
UPDATE questions SET status='RELEASED' WHERE id::text LIKE '40000000-%';
INSERT INTO content_release_records (id,question_id,from_status,to_status,validation_id,review_id,reason)
SELECT ('9' || substr(replace(q.id::text,'-',''),2))::uuid,q.id,'AI_REVIEWED','RELEASED',
       ('7' || substr(replace(q.id::text,'-',''),2))::uuid,('8' || substr(replace(q.id::text,'-',''),2))::uuid,'V1 curated demo release'
FROM questions q WHERE q.id::text LIKE '40000000-%';
