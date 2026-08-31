CREATE TABLE curriculum_sources (
    id uuid PRIMARY KEY,
    name text NOT NULL,
    source_type text NOT NULL CHECK (source_type IN ('INTERNAL_PRODUCT_SPEC', 'OFFICIAL_STANDARD', 'TEXTBOOK_MAPPING', 'OPEN_LICENSE')),
    version text NOT NULL,
    source_uri text,
    attribution text NOT NULL DEFAULT '',
    notes text NOT NULL DEFAULT '',
    status text NOT NULL DEFAULT 'DRAFT' CHECK (status IN ('DRAFT', 'RELEASED', 'RETIRED')),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (name, version)
);

CREATE TABLE knowledge_point_sources (
    knowledge_point_id uuid NOT NULL REFERENCES knowledge_points(id) ON DELETE CASCADE,
    curriculum_source_id uuid NOT NULL REFERENCES curriculum_sources(id),
    source_ref text NOT NULL,
    basis_kind text NOT NULL CHECK (basis_kind IN ('V1_SKELETON', 'TASK_019_DETAIL')),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (knowledge_point_id, curriculum_source_id, source_ref)
);

INSERT INTO curriculum_sources (id,name,source_type,version,source_uri,attribution,notes,status) VALUES
('60000000-0000-4000-8000-000000000001','互动式学习 V1 产品课程骨架','INTERNAL_PRODUCT_SPEC','V1.0','docs/product/互动式学习_V1.md','AI Learning Tutor 产品规格','内部产品课程骨架，不代表教育部官方课程标准或教材版本。','RELEASED');

CREATE FUNCTION v1_catalog_uuid(seed text) RETURNS uuid AS $$
DECLARE
    hash text := md5(seed);
BEGIN
    RETURN (substr(hash,1,8) || '-' || substr(hash,9,4) || '-5' || substr(hash,14,3) || '-a' || substr(hash,18,3) || '-' || substr(hash,21,12))::uuid;
END;
$$ LANGUAGE plpgsql IMMUTABLE STRICT;

CREATE TEMP TABLE v1_catalog_seed (
    subject_code text NOT NULL,
    grade_band_code text NOT NULL,
    domain_code text NOT NULL,
    domain_name text NOT NULL,
    domain_sort integer NOT NULL,
    unit_code text NOT NULL,
    unit_name text NOT NULL,
    unit_sort integer NOT NULL,
    knowledge_code text NOT NULL,
    knowledge_name text NOT NULL,
    default_difficulty text NOT NULL,
    source_ref text NOT NULL
) ON COMMIT DROP;

INSERT INTO v1_catalog_seed VALUES
-- Math, primary school: 22 skeleton entries.
('MATH','PRIMARY','PRIMARY_NUMBER_ALGEBRA','数与代数',10,'WHOLE_NUMBERS','整数与运算',10,'MATH-PRI-NUMBER-PLACE-VALUE','数与数位','L1','section-4.2/math/primary/数与数位'),
('MATH','PRIMARY','PRIMARY_NUMBER_ALGEBRA','数与代数',10,'WHOLE_NUMBERS','整数与运算',10,'MATH-PRI-FOUR-OPERATIONS','四则运算','L2','section-4.2/math/primary/四则运算'),
('MATH','PRIMARY','PRIMARY_NUMBER_ALGEBRA','数与代数',10,'WHOLE_NUMBERS','整数与运算',10,'MATH-PRI-OPERATION-ORDER','运算顺序','L2','section-4.2/math/primary/运算顺序'),
('MATH','PRIMARY','PRIMARY_NUMBER_ALGEBRA','数与代数',10,'DECIMALS_FRACTIONS','小数、分数与百分数',20,'MATH-PRI-DECIMALS','小数','L2','section-4.2/math/primary/小数'),
('MATH','PRIMARY','PRIMARY_NUMBER_ALGEBRA','数与代数',10,'DECIMALS_FRACTIONS','小数、分数与百分数',20,'MATH-PRI-FRACTIONS','分数','L2','section-4.2/math/primary/分数'),
('MATH','PRIMARY','PRIMARY_NUMBER_ALGEBRA','数与代数',10,'DECIMALS_FRACTIONS','小数、分数与百分数',20,'MATH-PRI-PERCENTAGES','百分数','L2','section-4.2/math/primary/百分数'),
('MATH','PRIMARY','PRIMARY_NUMBER_ALGEBRA','数与代数',10,'FACTORS_RATIOS','因数、倍数、比与比例',30,'MATH-PRI-FACTORS-MULTIPLES','因数与倍数','L2','section-4.2/math/primary/因数与倍数'),
('MATH','PRIMARY','PRIMARY_NUMBER_ALGEBRA','数与代数',10,'FACTORS_RATIOS','因数、倍数、比与比例',30,'MATH-PRI-RATIO','比','L2','section-4.2/math/primary/比'),
('MATH','PRIMARY','PRIMARY_NUMBER_ALGEBRA','数与代数',10,'FACTORS_RATIOS','因数、倍数、比与比例',30,'MATH-PRI-PROPORTION','比例','L3','section-4.2/math/primary/比例'),
('MATH','PRIMARY','PRIMARY_NUMBER_ALGEBRA','数与代数',10,'EQUATIONS','方程初步',40,'MATH-PRI-SIMPLE-EQUATION','简易方程','L2','section-4.2/math/primary/简易方程'),
('MATH','PRIMARY','PRIMARY_MEASUREMENT_GEOMETRY','图形与测量',20,'MEASUREMENT','量与单位',10,'MATH-PRI-UNIT-CONVERSION','单位换算','L2','section-4.2/math/primary/单位换算'),
('MATH','PRIMARY','PRIMARY_MEASUREMENT_GEOMETRY','图形与测量',20,'MEASUREMENT','量与单位',10,'MATH-PRI-LENGTH-TIME-MASS','长度/时间/质量','L2','section-4.2/math/primary/长度时间质量'),
('MATH','PRIMARY','PRIMARY_MEASUREMENT_GEOMETRY','图形与测量',20,'PLANE_MEASUREMENT','平面图形测量',20,'MATH-PRI-PERIMETER','周长','L2','section-4.2/math/primary/周长'),
('MATH','PRIMARY','PRIMARY_MEASUREMENT_GEOMETRY','图形与测量',20,'PLANE_MEASUREMENT','平面图形测量',20,'MATH-PRI-AREA','面积','L2','section-4.2/math/primary/面积'),
('MATH','PRIMARY','PRIMARY_MEASUREMENT_GEOMETRY','图形与测量',20,'SOLID_MEASUREMENT','立体图形测量',30,'MATH-PRI-VOLUME','体积','L3','section-4.2/math/primary/体积'),
('MATH','PRIMARY','PRIMARY_MEASUREMENT_GEOMETRY','图形与测量',20,'GEOMETRY','图形与位置',40,'MATH-PRI-ANGLE','角','L1','section-4.2/math/primary/角'),
('MATH','PRIMARY','PRIMARY_MEASUREMENT_GEOMETRY','图形与测量',20,'GEOMETRY','图形与位置',40,'MATH-PRI-PLANE-SHAPES','平面图形','L1','section-4.2/math/primary/平面图形'),
('MATH','PRIMARY','PRIMARY_MEASUREMENT_GEOMETRY','图形与测量',20,'GEOMETRY','图形与位置',40,'MATH-PRI-POSITION-DIRECTION','位置与方向','L2','section-4.2/math/primary/位置与方向'),
('MATH','PRIMARY','PRIMARY_DATA_MODELING','统计与问题解决',30,'STATISTICS','统计与随机',10,'MATH-PRI-AVERAGE','平均数','L2','section-4.2/math/primary/平均数'),
('MATH','PRIMARY','PRIMARY_DATA_MODELING','统计与问题解决',30,'STATISTICS','统计与随机',10,'MATH-PRI-STATISTICS-CHARTS','统计与图表','L2','section-4.2/math/primary/统计与图表'),
('MATH','PRIMARY','PRIMARY_DATA_MODELING','统计与问题解决',30,'STATISTICS','统计与随机',10,'MATH-PRI-PROBABILITY-INTUITION','概率直觉','L2','section-4.2/math/primary/概率直觉'),
('MATH','PRIMARY','PRIMARY_DATA_MODELING','统计与问题解决',30,'APPLICATION_MODELING','应用与建模',20,'MATH-PRI-WORD-PROBLEM-MODELING','应用题与信息建模','L3','section-4.2/math/primary/应用题与信息建模'),

-- Math, junior secondary: 26 new entries plus the preserved linear-equation seed below.
('MATH','JUNIOR_SECONDARY','JUNIOR_NUMBER_ALGEBRA','数与代数',40,'SIGNED_NUMBERS','有理数基础',10,'MATH-JUN-POSITIVE-NEGATIVE','正数/负数','L1','section-4.2/math/junior/正数负数'),
('MATH','JUNIOR_SECONDARY','JUNIOR_NUMBER_ALGEBRA','数与代数',40,'SIGNED_NUMBERS','有理数基础',10,'MATH-JUN-NUMBER-LINE','数轴','L1','section-4.2/math/junior/数轴'),
('MATH','JUNIOR_SECONDARY','JUNIOR_NUMBER_ALGEBRA','数与代数',40,'SIGNED_NUMBERS','有理数基础',10,'MATH-JUN-OPPOSITE-NUMBER','相反数','L1','section-4.2/math/junior/相反数'),
('MATH','JUNIOR_SECONDARY','JUNIOR_NUMBER_ALGEBRA','数与代数',40,'SIGNED_NUMBERS','有理数基础',10,'MATH-JUN-ABSOLUTE-VALUE','绝对值','L2','section-4.2/math/junior/绝对值'),
('MATH','JUNIOR_SECONDARY','JUNIOR_NUMBER_ALGEBRA','数与代数',40,'SIGNED_NUMBERS','有理数基础',10,'MATH-JUN-RATIONAL-OPERATIONS','有理数运算','L2','section-4.2/math/junior/有理数运算'),
('MATH','JUNIOR_SECONDARY','JUNIOR_NUMBER_ALGEBRA','数与代数',40,'ALGEBRAIC_EXPRESSIONS','代数式与整式',20,'MATH-JUN-ALGEBRAIC-EXPRESSION','代数式','L1','section-4.2/math/junior/代数式'),
('MATH','JUNIOR_SECONDARY','JUNIOR_NUMBER_ALGEBRA','数与代数',40,'ALGEBRAIC_EXPRESSIONS','代数式与整式',20,'MATH-JUN-POLYNOMIAL','整式','L2','section-4.2/math/junior/整式'),
('MATH','JUNIOR_SECONDARY','JUNIOR_NUMBER_ALGEBRA','数与代数',40,'ALGEBRAIC_EXPRESSIONS','代数式与整式',20,'MATH-JUN-COMBINE-LIKE-TERMS','合并同类项','L2','section-4.2/math/junior/合并同类项'),
('MATH','JUNIOR_SECONDARY','JUNIOR_NUMBER_ALGEBRA','数与代数',40,'ALGEBRAIC_EXPRESSIONS','代数式与整式',20,'MATH-JUN-REMOVE-PARENTHESES','去括号','L2','section-4.2/math/junior/去括号'),
('MATH','JUNIOR_SECONDARY','JUNIOR_NUMBER_ALGEBRA','数与代数',40,'EQUATIONS_INEQUALITIES','方程与不等式',30,'MATH-JUN-LINEAR-SYSTEM','二元一次方程组','L3','section-4.2/math/junior/二元一次方程组'),
('MATH','JUNIOR_SECONDARY','JUNIOR_NUMBER_ALGEBRA','数与代数',40,'EQUATIONS_INEQUALITIES','方程与不等式',30,'MATH-JUN-INEQUALITY','不等式','L3','section-4.2/math/junior/不等式'),
('MATH','JUNIOR_SECONDARY','JUNIOR_NUMBER_ALGEBRA','数与代数',40,'ALGEBRAIC_OPERATIONS','代数运算进阶',40,'MATH-JUN-POLYNOMIAL-MULTIPLICATION','整式乘法','L3','section-4.2/math/junior/整式乘法'),
('MATH','JUNIOR_SECONDARY','JUNIOR_NUMBER_ALGEBRA','数与代数',40,'ALGEBRAIC_OPERATIONS','代数运算进阶',40,'MATH-JUN-MULTIPLICATION-FORMULAS','乘法公式','L3','section-4.2/math/junior/乘法公式'),
('MATH','JUNIOR_SECONDARY','JUNIOR_NUMBER_ALGEBRA','数与代数',40,'ALGEBRAIC_OPERATIONS','代数运算进阶',40,'MATH-JUN-FACTORIZATION','因式分解','L3','section-4.2/math/junior/因式分解'),
('MATH','JUNIOR_SECONDARY','JUNIOR_NUMBER_ALGEBRA','数与代数',40,'ALGEBRAIC_OPERATIONS','代数运算进阶',40,'MATH-JUN-RATIONAL-EXPRESSION','分式','L3','section-4.2/math/junior/分式'),
('MATH','JUNIOR_SECONDARY','JUNIOR_NUMBER_ALGEBRA','数与代数',40,'ALGEBRAIC_OPERATIONS','代数运算进阶',40,'MATH-JUN-QUADRATIC-RADICAL','二次根式','L3','section-4.2/math/junior/二次根式'),
('MATH','JUNIOR_SECONDARY','JUNIOR_GEOMETRY','图形与几何',50,'COORDINATES','坐标与位置',10,'MATH-JUN-COORDINATE-SYSTEM','坐标系','L2','section-4.2/math/junior/坐标系'),
('MATH','JUNIOR_SECONDARY','JUNIOR_GEOMETRY','图形与几何',50,'TRIANGLES','三角形及其性质',20,'MATH-JUN-TRIANGLE','三角形','L2','section-4.2/math/junior/三角形'),
('MATH','JUNIOR_SECONDARY','JUNIOR_GEOMETRY','图形与几何',50,'TRIANGLES','三角形及其性质',20,'MATH-JUN-CONGRUENCE','全等','L3','section-4.2/math/junior/全等'),
('MATH','JUNIOR_SECONDARY','JUNIOR_GEOMETRY','图形与几何',50,'TRIANGLES','三角形及其性质',20,'MATH-JUN-AXIAL-SYMMETRY','轴对称','L2','section-4.2/math/junior/轴对称'),
('MATH','JUNIOR_SECONDARY','JUNIOR_GEOMETRY','图形与几何',50,'TRIANGLES','三角形及其性质',20,'MATH-JUN-ISOSCELES-TRIANGLE','等腰三角形','L3','section-4.2/math/junior/等腰三角形'),
('MATH','JUNIOR_SECONDARY','JUNIOR_GEOMETRY','图形与几何',50,'GEOMETRIC_RELATIONS','几何关系与证明',30,'MATH-JUN-PYTHAGOREAN-THEOREM','勾股定理','L3','section-4.2/math/junior/勾股定理'),
('MATH','JUNIOR_SECONDARY','JUNIOR_GEOMETRY','图形与几何',50,'GEOMETRIC_RELATIONS','几何关系与证明',30,'MATH-JUN-PARALLELOGRAM','平行四边形','L3','section-4.2/math/junior/平行四边形'),
('MATH','JUNIOR_SECONDARY','JUNIOR_FUNCTION_DATA','函数与数据',60,'FUNCTIONS','函数基础',10,'MATH-JUN-FUNCTION','函数','L2','section-4.2/math/junior/函数'),
('MATH','JUNIOR_SECONDARY','JUNIOR_FUNCTION_DATA','函数与数据',60,'FUNCTIONS','函数基础',10,'MATH-JUN-LINEAR-FUNCTION','一次函数','L3','section-4.2/math/junior/一次函数'),
('MATH','JUNIOR_SECONDARY','JUNIOR_FUNCTION_DATA','函数与数据',60,'DATA_ANALYSIS','数据分析',20,'MATH-JUN-DATA-ANALYSIS','数据分析','L2','section-4.2/math/junior/数据分析'),

-- Chinese, primary school: 12 skeleton entries.
('CHINESE','PRIMARY','PRIMARY_LANGUAGE_FOUNDATION','语言基础',10,'WORDS_SENTENCES','字词句与积累',10,'CHINESE-PRI-WORDS','字词','L1','section-5/chinese/primary/字词'),
('CHINESE','PRIMARY','PRIMARY_LANGUAGE_FOUNDATION','语言基础',10,'WORDS_SENTENCES','字词句与积累',10,'CHINESE-PRI-SENTENCE-COMPREHENSION','句子理解','L1','section-5/chinese/primary/句子理解'),
('CHINESE','PRIMARY','PRIMARY_LANGUAGE_FOUNDATION','语言基础',10,'WORDS_SENTENCES','字词句与积累',10,'CHINESE-PRI-LANGUAGE-ACCUMULATION','语言积累','L1','section-5/chinese/primary/语言积累'),
('CHINESE','PRIMARY','PRIMARY_READING','阅读理解',20,'BASIC_READING','段落与信息',10,'CHINESE-PRI-PARAGRAPH-COMPREHENSION','段落理解','L2','section-5/chinese/primary/段落理解'),
('CHINESE','PRIMARY','PRIMARY_READING','阅读理解',20,'BASIC_READING','段落与信息',10,'CHINESE-PRI-INFORMATION-EXTRACTION','信息提取','L2','section-5/chinese/primary/信息提取'),
('CHINESE','PRIMARY','PRIMARY_READING','阅读理解',20,'BASIC_READING','段落与信息',10,'CHINESE-PRI-MAIN-IDEA','阅读主旨','L2','section-5/chinese/primary/阅读主旨'),
('CHINESE','PRIMARY','PRIMARY_READING','阅读理解',20,'NARRATIVE_READING','人物、事件与关系',20,'CHINESE-PRI-CHARACTERS-EVENTS','人物与事件','L2','section-5/chinese/primary/人物与事件'),
('CHINESE','PRIMARY','PRIMARY_READING','阅读理解',20,'NARRATIVE_READING','人物、事件与关系',20,'CHINESE-PRI-CAUSE-EFFECT','因果关系','L2','section-5/chinese/primary/因果关系'),
('CHINESE','PRIMARY','PRIMARY_EXPRESSION_CULTURE','表达与文化',30,'EXPRESSION_WRITING','表达与写作',10,'CHINESE-PRI-RETELLING','表达与复述','L2','section-5/chinese/primary/表达与复述'),
('CHINESE','PRIMARY','PRIMARY_EXPRESSION_CULTURE','表达与文化',30,'EXPRESSION_WRITING','表达与写作',10,'CHINESE-PRI-WRITING-FOUNDATION','写话/作文基础','L2','section-5/chinese/primary/写话作文基础'),
('CHINESE','PRIMARY','PRIMARY_EXPRESSION_CULTURE','表达与文化',30,'CLASSICAL_CULTURE','诗歌与传统文化',20,'CHINESE-PRI-ANCIENT-POETRY','古诗基础','L2','section-5/chinese/primary/古诗基础'),
('CHINESE','PRIMARY','PRIMARY_EXPRESSION_CULTURE','表达与文化',30,'CLASSICAL_CULTURE','诗歌与传统文化',20,'CHINESE-PRI-TRADITIONAL-CULTURE','传统文化','L2','section-5/chinese/primary/传统文化'),

-- Chinese, junior secondary: 12 new entries plus two preserved seed entries below.
('CHINESE','JUNIOR_SECONDARY','JUNIOR_MODERN_READING','现代文阅读',40,'READING_GENRES','现代文体阅读',10,'CHINESE-JUN-MODERN-READING','现代文阅读','L2','section-5/chinese/junior/现代文阅读'),
('CHINESE','JUNIOR_SECONDARY','JUNIOR_MODERN_READING','现代文阅读',40,'READING_GENRES','现代文体阅读',10,'CHINESE-JUN-NARRATIVE','记叙文','L2','section-5/chinese/junior/记叙文'),
('CHINESE','JUNIOR_SECONDARY','JUNIOR_MODERN_READING','现代文阅读',40,'READING_GENRES','现代文体阅读',10,'CHINESE-JUN-EXPOSITORY','说明文','L2','section-5/chinese/junior/说明文'),
('CHINESE','JUNIOR_SECONDARY','JUNIOR_MODERN_READING','现代文阅读',40,'READING_GENRES','现代文体阅读',10,'CHINESE-JUN-ARGUMENTATIVE-FOUNDATION','议论文基础','L3','section-5/chinese/junior/议论文基础'),
('CHINESE','JUNIOR_SECONDARY','JUNIOR_MODERN_READING','现代文阅读',40,'EVIDENCE_ANALYSIS','信息、证据与分析',20,'CHINESE-JUN-SENTENCE-PARAGRAPH-ROLE','句段作用','L3','section-5/chinese/junior/句段作用'),
('CHINESE','JUNIOR_SECONDARY','JUNIOR_MODERN_READING','现代文阅读',40,'EVIDENCE_ANALYSIS','信息、证据与分析',20,'CHINESE-JUN-CHARACTER-THEME','人物与主题','L3','section-5/chinese/junior/人物与主题'),
('CHINESE','JUNIOR_SECONDARY','JUNIOR_LANGUAGE_EXPRESSION','语言与表达',50,'LANGUAGE_ART','语言艺术与运用',10,'CHINESE-JUN-RHETORIC','修辞','L2','section-5/chinese/junior/修辞'),
('CHINESE','JUNIOR_SECONDARY','JUNIOR_LANGUAGE_EXPRESSION','语言与表达',50,'LANGUAGE_ART','语言艺术与运用',10,'CHINESE-JUN-LANGUAGE-USE','语言运用','L2','section-5/chinese/junior/语言运用'),
('CHINESE','JUNIOR_SECONDARY','JUNIOR_LANGUAGE_EXPRESSION','语言与表达',50,'WRITING','写作',20,'CHINESE-JUN-WRITING','写作','L3','section-5/chinese/junior/写作'),
('CHINESE','JUNIOR_SECONDARY','JUNIOR_CLASSICS','古典与整本书阅读',60,'CLASSICAL_TEXT','文言诗词',10,'CHINESE-JUN-CLASSICAL-CHINESE','文言文','L3','section-5/chinese/junior/文言文'),
('CHINESE','JUNIOR_SECONDARY','JUNIOR_CLASSICS','古典与整本书阅读',60,'CLASSICAL_TEXT','文言诗词',10,'CHINESE-JUN-ANCIENT-POETRY','古诗词','L3','section-5/chinese/junior/古诗词'),
('CHINESE','JUNIOR_SECONDARY','JUNIOR_CLASSICS','古典与整本书阅读',60,'EXTENDED_READING','名著阅读',20,'CHINESE-JUN-LITERARY-CLASSICS','名著阅读','L3','section-5/chinese/junior/名著阅读'),

-- English, primary school: 7 skeleton entries.
('ENGLISH','PRIMARY','PRIMARY_LANGUAGE_FOUNDATION','语言基础',10,'FOUNDATION','语音、词汇与时态',10,'ENGLISH-PRI-PHONICS','Phonics 基础','L1','section-6/english/primary/phonics'),
('ENGLISH','PRIMARY','PRIMARY_LANGUAGE_FOUNDATION','语言基础',10,'FOUNDATION','语音、词汇与时态',10,'ENGLISH-PRI-COMMON-VOCABULARY','常用词汇','L1','section-6/english/primary/常用词汇'),
('ENGLISH','PRIMARY','PRIMARY_LANGUAGE_FOUNDATION','语言基础',10,'FOUNDATION','语音、词汇与时态',10,'ENGLISH-PRI-BASIC-TENSES','基础时态','L2','section-6/english/primary/基础时态'),
('ENGLISH','PRIMARY','PRIMARY_COMMUNICATION_LITERACY','沟通与读写',20,'EVERYDAY_COMMUNICATION','日常沟通',10,'ENGLISH-PRI-DAILY-SENTENCE-PATTERNS','日常句型','L1','section-6/english/primary/日常句型'),
('ENGLISH','PRIMARY','PRIMARY_COMMUNICATION_LITERACY','沟通与读写',20,'EVERYDAY_COMMUNICATION','日常沟通',10,'ENGLISH-PRI-LISTENING-READING','听读','L1','section-6/english/primary/听读'),
('ENGLISH','PRIMARY','PRIMARY_COMMUNICATION_LITERACY','沟通与读写',20,'READING_EXPRESSION','阅读与表达',20,'ENGLISH-PRI-SHORT-READING','简短阅读','L2','section-6/english/primary/简短阅读'),
('ENGLISH','PRIMARY','PRIMARY_COMMUNICATION_LITERACY','沟通与读写',20,'READING_EXPRESSION','阅读与表达',20,'ENGLISH-PRI-SIMPLE-EXPRESSION','简单表达','L2','section-6/english/primary/简单表达'),

-- English, junior secondary: 10 skeleton entries.
('ENGLISH','JUNIOR_SECONDARY','JUNIOR_LANGUAGE_KNOWLEDGE','语言知识',40,'VOCABULARY_GRAMMAR','词汇与语法',10,'ENGLISH-JUN-VOCABULARY','词汇','L2','section-6/english/junior/词汇'),
('ENGLISH','JUNIOR_SECONDARY','JUNIOR_LANGUAGE_KNOWLEDGE','语言知识',40,'VOCABULARY_GRAMMAR','词汇与语法',10,'ENGLISH-JUN-GRAMMAR','语法','L2','section-6/english/junior/语法'),
('ENGLISH','JUNIOR_SECONDARY','JUNIOR_LANGUAGE_KNOWLEDGE','语言知识',40,'TENSES_CLAUSES','时态与从句',20,'ENGLISH-JUN-TENSES','时态','L3','section-6/english/junior/时态'),
('ENGLISH','JUNIOR_SECONDARY','JUNIOR_LANGUAGE_KNOWLEDGE','语言知识',40,'TENSES_CLAUSES','时态与从句',20,'ENGLISH-JUN-CLAUSE-FOUNDATION','从句基础','L3','section-6/english/junior/从句基础'),
('ENGLISH','JUNIOR_SECONDARY','JUNIOR_COMPREHENSION','语篇理解',50,'READING_COMPREHENSION','阅读与完形',10,'ENGLISH-JUN-READING-COMPREHENSION','阅读理解','L2','section-6/english/junior/阅读理解'),
('ENGLISH','JUNIOR_SECONDARY','JUNIOR_COMPREHENSION','语篇理解',50,'READING_COMPREHENSION','阅读与完形',10,'ENGLISH-JUN-CLOZE-REASONING','完形思维','L3','section-6/english/junior/完形思维'),
('ENGLISH','JUNIOR_SECONDARY','JUNIOR_COMPREHENSION','语篇理解',50,'DISCOURSE','语篇结构',20,'ENGLISH-JUN-DISCOURSE-COMPREHENSION','语篇理解','L3','section-6/english/junior/语篇理解'),
('ENGLISH','JUNIOR_SECONDARY','JUNIOR_COMMUNICATION','综合运用',60,'WRITING_COMMUNICATION','写作与交流',10,'ENGLISH-JUN-WRITING','写作','L3','section-6/english/junior/写作'),
('ENGLISH','JUNIOR_SECONDARY','JUNIOR_COMMUNICATION','综合运用',60,'WRITING_COMMUNICATION','写作与交流',10,'ENGLISH-JUN-DAILY-COMMUNICATION','日常交流','L2','section-6/english/junior/日常交流'),
('ENGLISH','JUNIOR_SECONDARY','JUNIOR_COMMUNICATION','综合运用',60,'LISTENING_SPEAKING','听说',20,'ENGLISH-JUN-LISTENING-SPEAKING','听说','L2','section-6/english/junior/听说'),

-- Physics, junior secondary: 20 new entries plus speed and density preserved below.
('PHYSICS','JUNIOR_SECONDARY','MEASUREMENT_MOTION','测量与运动',10,'MEASUREMENT_MOTION','测量与机械运动',10,'PHYSICS-MEASUREMENT','测量','L1','section-7/physics/junior/测量'),
('PHYSICS','JUNIOR_SECONDARY','MEASUREMENT_MOTION','测量与运动',10,'MEASUREMENT_MOTION','测量与机械运动',10,'PHYSICS-MECHANICAL-MOTION','机械运动','L2','section-7/physics/junior/机械运动'),
('PHYSICS','JUNIOR_SECONDARY','WAVES_OPTICS','声与光',20,'SOUND','声音',10,'PHYSICS-SOUND','声音','L2','section-7/physics/junior/声音'),
('PHYSICS','JUNIOR_SECONDARY','WAVES_OPTICS','声与光',20,'OPTICS','光与透镜',20,'PHYSICS-LIGHT','光','L2','section-7/physics/junior/光'),
('PHYSICS','JUNIOR_SECONDARY','WAVES_OPTICS','声与光',20,'OPTICS','光与透镜',20,'PHYSICS-LENS','透镜','L3','section-7/physics/junior/透镜'),
('PHYSICS','JUNIOR_SECONDARY','MATTER_THERMAL','物质属性与热学',30,'MASS_DENSITY','质量与密度',10,'PHYSICS-MASS','质量','L1','section-7/physics/junior/质量'),
('PHYSICS','JUNIOR_SECONDARY','MATTER_THERMAL','物质属性与热学',30,'THERMAL','热学',20,'PHYSICS-THERMAL','热学','L2','section-7/physics/junior/热学'),
('PHYSICS','JUNIOR_SECONDARY','MECHANICS','力与机械',40,'FORCE_MOTION','力与运动',10,'PHYSICS-FORCE','力','L2','section-7/physics/junior/力'),
('PHYSICS','JUNIOR_SECONDARY','MECHANICS','力与机械',40,'FORCE_MOTION','力与运动',10,'PHYSICS-MOTION-AND-FORCE','运动与力','L3','section-7/physics/junior/运动与力'),
('PHYSICS','JUNIOR_SECONDARY','MECHANICS','力与机械',40,'PRESSURE_BUOYANCY','压强与浮力',20,'PHYSICS-PRESSURE','压强','L3','section-7/physics/junior/压强'),
('PHYSICS','JUNIOR_SECONDARY','MECHANICS','力与机械',40,'PRESSURE_BUOYANCY','压强与浮力',20,'PHYSICS-BUOYANCY','浮力','L3','section-7/physics/junior/浮力'),
('PHYSICS','JUNIOR_SECONDARY','MECHANICS','力与机械',40,'WORK_MACHINES','功、机械能与机械',30,'PHYSICS-WORK-MECHANICAL-ENERGY','功和机械能','L3','section-7/physics/junior/功和机械能'),
('PHYSICS','JUNIOR_SECONDARY','MECHANICS','力与机械',40,'WORK_MACHINES','功、机械能与机械',30,'PHYSICS-SIMPLE-MACHINES','简单机械','L3','section-7/physics/junior/简单机械'),
('PHYSICS','JUNIOR_SECONDARY','ELECTRICITY_MAGNETISM','电与磁',50,'BASIC_CIRCUITS','电路基础量',10,'PHYSICS-CURRENT','电流','L2','section-7/physics/junior/电流'),
('PHYSICS','JUNIOR_SECONDARY','ELECTRICITY_MAGNETISM','电与磁',50,'BASIC_CIRCUITS','电路基础量',10,'PHYSICS-VOLTAGE','电压','L2','section-7/physics/junior/电压'),
('PHYSICS','JUNIOR_SECONDARY','ELECTRICITY_MAGNETISM','电与磁',50,'BASIC_CIRCUITS','电路基础量',10,'PHYSICS-RESISTANCE','电阻','L2','section-7/physics/junior/电阻'),
('PHYSICS','JUNIOR_SECONDARY','ELECTRICITY_MAGNETISM','电与磁',50,'CIRCUIT_LAWS','电路规律与用电',20,'PHYSICS-OHMS-LAW','欧姆定律','L3','section-7/physics/junior/欧姆定律'),
('PHYSICS','JUNIOR_SECONDARY','ELECTRICITY_MAGNETISM','电与磁',50,'CIRCUIT_LAWS','电路规律与用电',20,'PHYSICS-ELECTRIC-POWER','电功率','L3','section-7/physics/junior/电功率'),
('PHYSICS','JUNIOR_SECONDARY','ELECTRICITY_MAGNETISM','电与磁',50,'CIRCUIT_LAWS','电路规律与用电',20,'PHYSICS-HOUSEHOLD-CIRCUITS','家庭电路','L3','section-7/physics/junior/家庭电路'),
('PHYSICS','JUNIOR_SECONDARY','ELECTRICITY_MAGNETISM','电与磁',50,'MAGNETISM','磁现象',30,'PHYSICS-MAGNETISM','磁','L2','section-7/physics/junior/磁'),

-- Chemistry, junior secondary: 18 skeleton entries.
('CHEMISTRY','JUNIOR_SECONDARY','EXPERIMENT_MATTER','实验与常见物质',10,'FOUNDATIONS','化学基础与实验',10,'CHEMISTRY-MATTER-CHANGE','物质与变化','L1','section-8/chemistry/junior/物质与变化'),
('CHEMISTRY','JUNIOR_SECONDARY','EXPERIMENT_MATTER','实验与常见物质',10,'FOUNDATIONS','化学基础与实验',10,'CHEMISTRY-BASIC-EXPERIMENTS','实验基本操作','L1','section-8/chemistry/junior/实验基本操作'),
('CHEMISTRY','JUNIOR_SECONDARY','EXPERIMENT_MATTER','实验与常见物质',10,'COMMON_SUBSTANCES','空气、氧气与水',20,'CHEMISTRY-AIR','空气','L2','section-8/chemistry/junior/空气'),
('CHEMISTRY','JUNIOR_SECONDARY','EXPERIMENT_MATTER','实验与常见物质',10,'COMMON_SUBSTANCES','空气、氧气与水',20,'CHEMISTRY-OXYGEN','氧气','L2','section-8/chemistry/junior/氧气'),
('CHEMISTRY','JUNIOR_SECONDARY','EXPERIMENT_MATTER','实验与常见物质',10,'COMMON_SUBSTANCES','空气、氧气与水',20,'CHEMISTRY-WATER','水','L2','section-8/chemistry/junior/水'),
('CHEMISTRY','JUNIOR_SECONDARY','MICRO_SYMBOLS','微观世界与化学用语',20,'MICRO_WORLD','分子、原子与元素',10,'CHEMISTRY-MOLECULES-ATOMS','分子与原子','L2','section-8/chemistry/junior/分子与原子'),
('CHEMISTRY','JUNIOR_SECONDARY','MICRO_SYMBOLS','微观世界与化学用语',20,'MICRO_WORLD','分子、原子与元素',10,'CHEMISTRY-ELEMENTS','元素','L2','section-8/chemistry/junior/元素'),
('CHEMISTRY','JUNIOR_SECONDARY','MICRO_SYMBOLS','微观世界与化学用语',20,'CHEMICAL_LANGUAGE','化学式与化合价',20,'CHEMISTRY-CHEMICAL-FORMULA','化学式','L2','section-8/chemistry/junior/化学式'),
('CHEMISTRY','JUNIOR_SECONDARY','MICRO_SYMBOLS','微观世界与化学用语',20,'CHEMICAL_LANGUAGE','化学式与化合价',20,'CHEMISTRY-VALENCE','化合价','L3','section-8/chemistry/junior/化合价'),
('CHEMISTRY','JUNIOR_SECONDARY','LAWS_REACTIONS','化学反应与规律',30,'REACTION_LAWS','质量守恒与方程式',10,'CHEMISTRY-MASS-CONSERVATION','质量守恒','L3','section-8/chemistry/junior/质量守恒'),
('CHEMISTRY','JUNIOR_SECONDARY','LAWS_REACTIONS','化学反应与规律',30,'REACTION_LAWS','质量守恒与方程式',10,'CHEMISTRY-CHEMICAL-EQUATION','化学方程式','L3','section-8/chemistry/junior/化学方程式'),
('CHEMISTRY','JUNIOR_SECONDARY','LAWS_REACTIONS','化学反应与规律',30,'CARBON_COMBUSTION','碳与燃烧',20,'CHEMISTRY-CARBON','碳','L2','section-8/chemistry/junior/碳'),
('CHEMISTRY','JUNIOR_SECONDARY','LAWS_REACTIONS','化学反应与规律',30,'CARBON_COMBUSTION','碳与燃烧',20,'CHEMISTRY-COMBUSTION','燃烧','L2','section-8/chemistry/junior/燃烧'),
('CHEMISTRY','JUNIOR_SECONDARY','CHEMISTRY_APPLICATION','物质应用与生活化学',40,'MATERIALS','金属与酸碱盐',10,'CHEMISTRY-METALS','金属','L2','section-8/chemistry/junior/金属'),
('CHEMISTRY','JUNIOR_SECONDARY','CHEMISTRY_APPLICATION','物质应用与生活化学',40,'MATERIALS','金属与酸碱盐',10,'CHEMISTRY-ACIDS-BASES-SALTS','酸碱盐','L3','section-8/chemistry/junior/酸碱盐'),
('CHEMISTRY','JUNIOR_SECONDARY','CHEMISTRY_APPLICATION','物质应用与生活化学',40,'SOLUTIONS','溶液与浓度',20,'CHEMISTRY-SOLUTIONS','溶液','L2','section-8/chemistry/junior/溶液'),
('CHEMISTRY','JUNIOR_SECONDARY','CHEMISTRY_APPLICATION','物质应用与生活化学',40,'SOLUTIONS','溶液与浓度',20,'CHEMISTRY-CONCENTRATION','浓度','L3','section-8/chemistry/junior/浓度'),
('CHEMISTRY','JUNIOR_SECONDARY','CHEMISTRY_APPLICATION','物质应用与生活化学',40,'CHEMISTRY_LIFE','化学与生活',30,'CHEMISTRY-AND-LIFE','化学与生活','L2','section-8/chemistry/junior/化学与生活');

INSERT INTO domains (id,subject_id,code,name,description,sort_order)
SELECT DISTINCT v1_catalog_uuid('domain:' || seed.subject_code || ':' || seed.domain_code), subject.id,
       seed.domain_code, seed.domain_name, seed.domain_name || '课程领域', seed.domain_sort
FROM v1_catalog_seed seed
JOIN subjects subject ON subject.code=seed.subject_code
ON CONFLICT (subject_id,code) DO NOTHING;

INSERT INTO units (id,domain_id,code,name,grade_band_code,sort_order)
SELECT DISTINCT v1_catalog_uuid('unit:' || seed.subject_code || ':' || seed.grade_band_code || ':' || seed.domain_code || ':' || seed.unit_code),
       domain.id, seed.grade_band_code || '_' || seed.unit_code, seed.unit_name, seed.grade_band_code, seed.unit_sort
FROM v1_catalog_seed seed
JOIN subjects subject ON subject.code=seed.subject_code
JOIN domains domain ON domain.subject_id=subject.id AND domain.code=seed.domain_code
ON CONFLICT (domain_id,code) DO NOTHING;

INSERT INTO knowledge_points
(id,subject_id,domain_id,unit_id,grade_band_code,code,name,description,why_it_matters_json,default_difficulty,status,curriculum_version)
SELECT v1_catalog_uuid('knowledge-point:' || seed.knowledge_code), subject.id, domain.id, unit.id,
       seed.grade_band_code, seed.knowledge_code, seed.knowledge_name,
       'V1 ' || grade_band.name_zh || subject.name_zh || '课程骨架：' || seed.knowledge_name,
       jsonb_build_object(
           'daily_life','在真实情境中识别和运用' || seed.knowledge_name,
           'human_world','用' || seed.knowledge_name || '理解信息、现象与表达',
           'future_learning','为' || seed.unit_name || '后续学习建立基础',
           'career_or_science','支持与' || seed.domain_name || '相关的进一步学习'
       ),
       seed.default_difficulty,'RELEASED','v1-full-catalog'
FROM v1_catalog_seed seed
JOIN subjects subject ON subject.code=seed.subject_code
JOIN grade_bands grade_band ON grade_band.code=seed.grade_band_code
JOIN domains domain ON domain.subject_id=subject.id AND domain.code=seed.domain_code
JOIN units unit ON unit.domain_id=domain.id AND unit.code=seed.grade_band_code || '_' || seed.unit_code
ON CONFLICT (code) DO NOTHING;

INSERT INTO knowledge_point_sources (knowledge_point_id,curriculum_source_id,source_ref,basis_kind)
SELECT knowledge_point.id,'60000000-0000-4000-8000-000000000001',seed.source_ref,'V1_SKELETON'
FROM v1_catalog_seed seed
JOIN knowledge_points knowledge_point ON knowledge_point.code=seed.knowledge_code;

-- Preserve the five exact Task 019 knowledge points as the canonical V1 skeleton rows.
WITH preserved(id,domain_code,unit_code,source_ref) AS (VALUES
    ('30000000-0000-4000-8000-000000000001'::uuid,'JUNIOR_NUMBER_ALGEBRA','JUNIOR_SECONDARY_EQUATIONS_INEQUALITIES','section-4.2/math/junior/一元一次方程'),
    ('30000000-0000-4000-8000-000000000004'::uuid,'JUNIOR_MODERN_READING','JUNIOR_SECONDARY_EVIDENCE_ANALYSIS','section-5/chinese/junior/信息提取'),
    ('30000000-0000-4000-8000-000000000005'::uuid,'JUNIOR_MODERN_READING','JUNIOR_SECONDARY_EVIDENCE_ANALYSIS','section-5/chinese/junior/证据定位'),
    ('30000000-0000-4000-8000-000000000010'::uuid,'MEASUREMENT_MOTION','JUNIOR_SECONDARY_MEASUREMENT_MOTION','section-7/physics/junior/速度'),
    ('30000000-0000-4000-8000-000000000011'::uuid,'MATTER_THERMAL','JUNIOR_SECONDARY_MASS_DENSITY','section-7/physics/junior/密度')
)
UPDATE knowledge_points knowledge_point
SET domain_id=domain.id,unit_id=unit.id,curriculum_version='v1-full-catalog',updated_at=now()
FROM preserved
JOIN domains domain ON domain.code=preserved.domain_code
JOIN units unit ON unit.domain_id=domain.id AND unit.code=preserved.unit_code
WHERE knowledge_point.id=preserved.id;

INSERT INTO knowledge_point_sources (knowledge_point_id,curriculum_source_id,source_ref,basis_kind) VALUES
('30000000-0000-4000-8000-000000000001','60000000-0000-4000-8000-000000000001','section-4.2/math/junior/一元一次方程','V1_SKELETON'),
('30000000-0000-4000-8000-000000000004','60000000-0000-4000-8000-000000000001','section-5/chinese/junior/信息提取','V1_SKELETON'),
('30000000-0000-4000-8000-000000000005','60000000-0000-4000-8000-000000000001','section-5/chinese/junior/证据定位','V1_SKELETON'),
('30000000-0000-4000-8000-000000000010','60000000-0000-4000-8000-000000000001','section-7/physics/junior/速度','V1_SKELETON'),
('30000000-0000-4000-8000-000000000011','60000000-0000-4000-8000-000000000001','section-7/physics/junior/密度','V1_SKELETON');

-- Keep every high-quality demo detail addressable, while distinguishing it from the broad V1 skeleton.
INSERT INTO knowledge_point_sources (knowledge_point_id,curriculum_source_id,source_ref,basis_kind)
SELECT id,'60000000-0000-4000-8000-000000000001','task-019-detail/' || code,'TASK_019_DETAIL'
FROM knowledge_points
WHERE id::text LIKE '30000000-%';

-- Move the remaining detailed demo points into the canonical hierarchy without changing their stable IDs.
WITH placement(id,domain_code,unit_code) AS (VALUES
    ('30000000-0000-4000-8000-000000000002'::uuid,'JUNIOR_NUMBER_ALGEBRA','JUNIOR_SECONDARY_ALGEBRAIC_OPERATIONS'),
    ('30000000-0000-4000-8000-000000000003'::uuid,'JUNIOR_FUNCTION_DATA','JUNIOR_SECONDARY_DATA_ANALYSIS'),
    ('30000000-0000-4000-8000-000000000006'::uuid,'JUNIOR_CLASSICS','JUNIOR_SECONDARY_CLASSICAL_TEXT'),
    ('30000000-0000-4000-8000-000000000007'::uuid,'JUNIOR_LANGUAGE_KNOWLEDGE','JUNIOR_SECONDARY_VOCABULARY_GRAMMAR'),
    ('30000000-0000-4000-8000-000000000008'::uuid,'JUNIOR_LANGUAGE_KNOWLEDGE','JUNIOR_SECONDARY_TENSES_CLAUSES'),
    ('30000000-0000-4000-8000-000000000009'::uuid,'JUNIOR_COMPREHENSION','JUNIOR_SECONDARY_READING_COMPREHENSION'),
    ('30000000-0000-4000-8000-000000000012'::uuid,'MECHANICS','JUNIOR_SECONDARY_FORCE_MOTION'),
    ('30000000-0000-4000-8000-000000000013'::uuid,'EXPERIMENT_MATTER','JUNIOR_SECONDARY_FOUNDATIONS'),
    ('30000000-0000-4000-8000-000000000014'::uuid,'EXPERIMENT_MATTER','JUNIOR_SECONDARY_COMMON_SUBSTANCES'),
    ('30000000-0000-4000-8000-000000000015'::uuid,'MICRO_SYMBOLS','JUNIOR_SECONDARY_MICRO_WORLD')
)
UPDATE knowledge_points knowledge_point
SET domain_id=domain.id,unit_id=unit.id,curriculum_version='v1-full-catalog',updated_at=now()
FROM placement
JOIN domains domain ON domain.code=placement.domain_code
JOIN units unit ON unit.domain_id=domain.id AND unit.code=placement.unit_code
WHERE knowledge_point.id=placement.id;

DROP FUNCTION v1_catalog_uuid(text);
