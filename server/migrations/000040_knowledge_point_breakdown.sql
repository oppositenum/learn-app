-- §1.1 asks every knowledge point for three breakdown items: what it builds
-- on, where it is hard, and where children commonly get stuck. They describe
-- the knowledge point for the Owner, so they live on knowledge_points and not
-- in any question's private misconceptions. Knowledge points without a
-- written breakdown stay NULL.
ALTER TABLE knowledge_points
    ADD COLUMN foundation text,
    ADD COLUMN difficulty_points text,
    ADD COLUMN common_stuck_point text;

-- MATH-LINEAR-EQUATION takes the breakdown already written for it in §1.1.
UPDATE knowledge_points
SET foundation = '四则运算熟练、负数概念、等式性质',
    difficulty_points = '把文字问题翻译成方程；等式两边同时运算；检验答案',
    common_stuck_point = '不会算不是主要问题，而是看不到题目里的等量关系，所以不知道为什么要列方程'
WHERE code = 'MATH-LINEAR-EQUATION';
