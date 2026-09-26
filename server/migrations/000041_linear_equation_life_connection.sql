-- MATH-LINEAR-EQUATION gets its life connection written as sentences a child
-- can read, replacing the four short words it had. Its three world_connections
-- rows, generated from those words, take the matching sentence as their
-- explanation and the examples in it as their title. The rows are shown, not
-- practised: they are not questions and never enter classroom task selection.
-- Other knowledge points keep what they have.
UPDATE knowledge_points
SET why_it_matters_json = jsonb_build_object(
        'daily_life', '用来把固定费用和按数量增加的费用分开，比如门票加服务费、租车起步价加每公里费用。',
        'human_world', '用来比较两种计费哪个更合适，比如两家打印店、两种租车方案。',
        'future_learning', '后面学习一次函数时，会用这里的固定起点和每增加 1 份的变化。',
        'career_or_science', '工程、财务和实验记录里，常用这种固定量加相同变化量来估算。'
    ),
    updated_at = now()
WHERE code = 'MATH-LINEAR-EQUATION';

UPDATE world_connections wc
SET title = CASE wc.connection_type
        WHEN 'DAILY_LIFE' THEN '门票加服务费、租车起步价加每公里费用'
        WHEN 'HUMAN_WORLD' THEN '两家打印店、两种租车方案'
        WHEN 'SCIENCE_OR_CAREER' THEN '工程、财务和实验记录'
    END,
    explanation = CASE wc.connection_type
        WHEN 'DAILY_LIFE' THEN knowledge_point.why_it_matters_json->>'daily_life'
        WHEN 'HUMAN_WORLD' THEN knowledge_point.why_it_matters_json->>'human_world'
        WHEN 'SCIENCE_OR_CAREER' THEN knowledge_point.why_it_matters_json->>'career_or_science'
    END
FROM knowledge_points knowledge_point
WHERE knowledge_point.id = wc.knowledge_point_id
  AND knowledge_point.code = 'MATH-LINEAR-EQUATION';
