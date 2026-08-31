INSERT INTO knowledge_points
(id,subject_id,domain_id,unit_id,grade_band_code,code,name,description,why_it_matters_json,default_difficulty,status,curriculum_version)
SELECT '71000000-0000-4000-8000-000000000001',subject.id,domain.id,unit.id,'JUNIOR_SECONDARY',
       'PHYSICS-FORCE-CONCEPT','力','V1 初中物理课程骨架：力',
       '{"daily_life":"在真实情境中识别力","human_world":"用力理解运动与相互作用现象","future_learning":"为力与运动后续学习建立基础","career_or_science":"支持与力学相关的进一步学习"}',
       'L2','RELEASED','v1-full-catalog'
FROM subjects subject
JOIN domains domain ON domain.subject_id=subject.id AND domain.code='MECHANICS'
JOIN units unit ON unit.domain_id=domain.id AND unit.code='JUNIOR_SECONDARY_FORCE_MOTION'
WHERE subject.code='PHYSICS';

DELETE FROM knowledge_point_sources
WHERE knowledge_point_id='30000000-0000-4000-8000-000000000012'
  AND curriculum_source_id='60000000-0000-4000-8000-000000000001'
  AND source_ref='section-7/physics/junior/力'
  AND basis_kind='V1_SKELETON';

INSERT INTO knowledge_point_sources (knowledge_point_id,curriculum_source_id,source_ref,basis_kind) VALUES
('71000000-0000-4000-8000-000000000001','60000000-0000-4000-8000-000000000001','section-7/physics/junior/力','V1_SKELETON');
