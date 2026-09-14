-- B7-4: establish an auditable, knowledge-point-scoped diagnostic taxonomy
-- for the activated linear-equation lineage. ON CONFLICT keeps this migration
-- safe to replay in development and test databases.
INSERT INTO misconceptions (id, code, name, description, diagnosis_signals_json)
VALUES
    ('e0000000-0000-4000-8000-000000000016', 'LINEAR_CHANGE_NOT_IDENTIFIED', '未识别单位变化量', '观察表格或生活记录时，未识别每增加一个单位所产生的固定变化量。', '["misses equal adjacent differences","cannot identify per-unit change"]'),
    ('e0000000-0000-4000-8000-000000000017', 'FIXED_START_NOT_IDENTIFIED', '未识别固定起点', '把不随自变量变化的一次性起点或固定费用当成随单位重复变化的量。', '["misses fixed starting value","repeats one-time charge"]'),
    ('e0000000-0000-4000-8000-000000000018', 'LINEAR_TERMS_CONFUSED', '混淆系数项与固定项', '把每单位变化的系数、固定项和总量在一次关系式中的结构对应错。', '["confuses coefficient and constant","mis-maps equation terms"]'),
    ('e0000000-0000-4000-8000-000000000019', 'LINEAR_ISOLATION_ERROR', '移项或系数处理错误', '求解一元一次方程时移项变号错误，或未正确用系数除以等式两边。', '["transposes with wrong sign","fails to divide by coefficient"]'),
    ('e0000000-0000-4000-8000-000000000020', 'MULTIPLIES_INSTEAD_OF_DIVIDES', '除法关系误用乘法', '从总量和单位数求一个单位的量时，把应做的除法误写成乘法。', '["multiplies instead of dividing","reverses division relationship"]')
ON CONFLICT (code) DO UPDATE SET
    name = EXCLUDED.name,
    description = EXCLUDED.description,
    diagnosis_signals_json = EXCLUDED.diagnosis_signals_json;

INSERT INTO knowledge_misconception_links (knowledge_point_id, misconception_id)
SELECT '30000000-0000-4000-8000-000000000001'::uuid, misconception.id
FROM misconceptions misconception
WHERE misconception.code IN (
    'FIXED_COST_IGNORED',
    'LINEAR_CHANGE_NOT_IDENTIFIED',
    'FIXED_START_NOT_IDENTIFIED',
    'LINEAR_TERMS_CONFUSED',
    'LINEAR_ISOLATION_ERROR',
    'MULTIPLIES_INSTEAD_OF_DIVIDES'
)
ON CONFLICT (knowledge_point_id, misconception_id) DO NOTHING;
