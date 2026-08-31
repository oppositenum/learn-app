INSERT INTO misconceptions (id,code,name,description,diagnosis_signals_json) VALUES
('e0000000-0000-4000-8000-000000000001','FIXED_COST_IGNORED','忽略固定费用','建立数量关系时遗漏不随数量变化的固定项','["omits fixed term"]'),
('e0000000-0000-4000-8000-000000000002','ADD_DENOMINATORS_DIRECTLY','分母直接相加','没有先建立共同计数单位','["adds denominators"]'),
('e0000000-0000-4000-8000-000000000003','DISCOUNT_AS_ABSOLUTE','把折扣当绝对量','混淆比例和减少的金额','["subtracts discount number"]'),
('e0000000-0000-4000-8000-000000000004','OMITS_CONDITION','遗漏关键信息','提取信息时漏掉题目指定条件','["missing field"]'),
('e0000000-0000-4000-8000-000000000005','OPINION_WITHOUT_EVIDENCE','判断缺少证据','给出观点但不能定位原文依据','["no quote"]'),
('e0000000-0000-4000-8000-000000000006','PARAPHRASE_WITHOUT_IMAGE','忽略意象','只翻译诗句而没有识别景物','["no imagery"]'),
('e0000000-0000-4000-8000-000000000007','THERE_AGREEMENT','There be 单复数错误','没有依据紧邻名词选择 is 或 are','["number mismatch"]'),
('e0000000-0000-4000-8000-000000000008','TENSE_BY_CURRENT_TIME','按当前时间选时态','忽略频率词表达的习惯性','["ignores frequency marker"]'),
('e0000000-0000-4000-8000-000000000009','CONFUSES_OPENING_TIME','混淆相邻时间','没有根据目标事件定位细节','["selects nearby time"]'),
('e0000000-0000-4000-8000-000000000010','MULTIPLIES_DISTANCE_TIME','速度关系错误','把路程和时间相乘而不是求单位时间路程','["multiplies distance time"]'),
('e0000000-0000-4000-8000-000000000011','COMPARES_MASS_ONLY','只比较质量','忽略体积对密度比较的影响','["mass only"]'),
('e0000000-0000-4000-8000-000000000012','FORCE_ONLY_DEFORMS','力只产生形变','忽略力可以改变运动状态','["deformation only"]'),
('e0000000-0000-4000-8000-000000000013','STATE_CHANGE_IS_CHEMICAL','把状态变化当化学变化','没有用是否生成新物质判断','["state means chemical"]'),
('e0000000-0000-4000-8000-000000000014','AIR_IS_ONLY_OXYGEN','把空气等同氧气','没有根据实验体积理解空气组成','["air equals oxygen"]'),
('e0000000-0000-4000-8000-000000000015','SYMBOL_IS_SUBSTANCE_SAMPLE','符号与物质混淆','混淆元素符号和具体物质样品','["symbol means object"]');

INSERT INTO knowledge_misconception_links (knowledge_point_id,misconception_id)
SELECT ('30000000-0000-4000-8000-' || lpad(sequence::text,12,'0'))::uuid,
       ('e0000000-0000-4000-8000-' || lpad(sequence::text,12,'0'))::uuid
FROM generate_series(1,15) AS sequence;
