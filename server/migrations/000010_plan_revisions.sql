ALTER TABLE learning_plans DROP CONSTRAINT learning_plans_student_id_plan_date_status_key;
CREATE UNIQUE INDEX learning_plans_one_current_per_day
ON learning_plans(student_id,plan_date)
WHERE status IN ('PROPOSED','ACTIVE');
