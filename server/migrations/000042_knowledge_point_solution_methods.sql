-- Solution methods of a knowledge point, each answered through the same five
-- questions. They are prepared for the Owner only: no Student API reads this
-- table and the classroom does not show it. Only MATH-LINEAR-EQUATION has rows,
-- written from the two strategies of §6.1.
CREATE TABLE knowledge_point_solution_methods (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    knowledge_point_id uuid NOT NULL REFERENCES knowledge_points(id) ON DELETE CASCADE,
    sequence integer NOT NULL CHECK (sequence > 0),
    method_name text NOT NULL CHECK (method_name <> ''),
    first_look text NOT NULL CHECK (first_look <> ''),
    why_this_method text NOT NULL CHECK (why_this_method <> ''),
    method_path text NOT NULL CHECK (method_path <> ''),
    check_where text NOT NULL CHECK (check_where <> ''),
    more_direct text NOT NULL CHECK (more_direct <> ''),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (knowledge_point_id, sequence)
);

INSERT INTO knowledge_point_solution_methods (knowledge_point_id,sequence,method_name,first_look,why_this_method,method_path,check_where,more_direct)
SELECT kp.id, method.sequence, method.method_name, method.first_look, method.why_this_method, method.method_path, method.check_where, method.more_direct
FROM knowledge_points kp
CROSS JOIN (VALUES
    (1, '假设法',
     '先看一共有多少个头、一共有多少只脚。',
     '先当成全是鸡，脚的差额就知道兔子有几只，不用一开始列两个未知数。',
     '全假设，算差额，用每只多出来的脚数去除，再得到另一种。',
     '用两种数量分别乘脚数，加起来是否等于总脚数。',
     '熟练以后可以直接列方程。'),
    (2, '列方程法',
     '先看哪个量不知道，哪个量和它按固定关系一起变。',
     '关系已经是一次的，设未知数比反复试数更清楚。',
     '设未知数，写固定量加每份变化量，让它等于总量，再解。',
     '把求出的数代回原式，看等号两边是否相同。',
     '数字很小的时候也可以画图或列表。')
) AS method(sequence, method_name, first_look, why_this_method, method_path, check_where, more_direct)
WHERE kp.code = 'MATH-LINEAR-EQUATION';
