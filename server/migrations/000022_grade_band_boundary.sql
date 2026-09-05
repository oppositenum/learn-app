CREATE TEMP TABLE invalid_available_grade_blocks ON COMMIT DROP AS
SELECT block.id, block.plan_id
FROM learning_plan_blocks block
JOIN learning_plans plan ON plan.id=block.plan_id
JOIN students student ON student.id=plan.student_id
JOIN knowledge_points knowledge_point ON knowledge_point.id=block.knowledge_point_id
JOIN grade_bands grade_band ON grade_band.code=knowledge_point.grade_band_code
WHERE block.status='AVAILABLE'
  AND grade_band.min_grade>student.grade_level;

DELETE FROM learning_plan_blocks block
USING invalid_available_grade_blocks invalid
WHERE block.id=invalid.id;

DELETE FROM learning_plans plan
USING (SELECT DISTINCT plan_id FROM invalid_available_grade_blocks) affected
WHERE plan.id=affected.plan_id
  AND plan.status='PROPOSED'
  AND NOT EXISTS (
      SELECT 1 FROM learning_plan_blocks remaining WHERE remaining.plan_id=plan.id
  );
