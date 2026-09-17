# 广州教材对齐的课程骨架与自产题库（P0 取证阶段）

## 1. 基线与范围

- 基线分支：`feat/provider-identity-and-prompts`
- 起始 SHA：`58861290539edd1d70d28c83a788875c24f778fd`
- 结束 SHA：`58861290539edd1d70d28c83a788875c24f778fd`（本期仅新增本报告）
- 查阅日期：2026-09-17（Asia/Shanghai）
- 范围：广州教材版本公开信息取证、内部课程骨架差距分析、自产题库方案；不写业务代码。

## 2. 教材版本事实（官方来源）

广州教育局公开文件确认义务教育课程按国家课程方案和学科课程标准实施，并要求教材作为学校教学的重要资源；但截至查阅日，未找到市教育局公开的、按“学科 × 小学/初中 × 册次/版本”列出的统一教材征订目录。因此以下版本不得推断，均为 `UNVERIFIED`，需负责人向广州教育局或具体学校索取当学年征订单确认。

| 学科 | 小学（1–6） | 初中（7–9） | 官方公开依据/状态 |
|---|---|---|---|
| 数学 | `UNVERIFIED`：教材出版社、册次、版本待征订单确认 | `UNVERIFIED`：教材出版社、册次、版本待征订单确认 | 广州市教育局《义务教育课程计划（2025年修订）》确认课程实施要求，但未列教材版本；查阅 2026-09-17 |
| 语文 | `UNVERIFIED`：教材出版社、册次、版本待征订单确认 | `UNVERIFIED`：教材出版社、册次、版本待征订单确认 | 同上；广州中考文件确认语文为义务教育阶段考试科目，不等于教材版本；查阅 2026-09-17 |
| 英语 | `UNVERIFIED`：教材出版社、册次、版本待征订单确认 | `UNVERIFIED`：教材出版社、册次、版本待征订单确认 | 同上；查阅 2026-09-17 |
| 物理 | 不适用：项目骨架仅在初中设物理 | `UNVERIFIED`：教材出版社、册次、版本待征订单确认 | 广州公开中考方案确认物理科目及实验操作要求；未列教材版本；查阅 2026-09-17 |
| 化学 | 不适用：项目骨架仅在初中设化学 | `UNVERIFIED`：教材出版社、册次、版本待征订单确认 | 广州公开中考方案确认化学科目及实验操作要求；未列教材版本；查阅 2026-09-17 |

官方链接：广州市教育局《广州市义务教育课程计划（2025年修订）》<https://jyj.gz.gov.cn/gk/zfxxgkml/bmwj/qtwj/content/post_10364457.html>；广州市教育局《2027—2029年广州市初中学业水平考试录取计分科目考试实施方案》<https://jyj.gz.gov.cn/gk/zfxxgkml/ywgz/zsks/content/post_10581398.html>。两者只证明课程/考试政策，不证明具体教材授权或版本。

## 3. 授权清单（阻断项）

| 类别 | 可用范围 | 依据与处理 |
|---|---|---|
| 可直接使用 | 国家公开课程方案、课程标准及广州公开课程实施政策 | 公开政府文件；登记为 `OFFICIAL_STANDARD`，保留 URL、版本、查阅日期和归属。不得复制教材正文。 |
| 可映射不可复制 | 经负责人确认的教材目录、章节顺序、知识点归属 | 只记录“教材版本/册次/章节 → 内部 knowledge point”的映射关系；不得存正文、插图、例题原文。需 `TEXTBOOK_MAPPING` 来源记录和负责人提供的授权/征订单。 |
| 不得使用 | 商业教辅、商业题库、真题卷、练习册、扫描件、无明确授权的讲义或网络转载内容 | 不抓取、不下载、不导入。负责人已有授权材料由负责人提供，项目不自行寻找替代品。 |

## 4. 仓库事实 A-F 复核

### A. `curriculum_sources`

**相符。** `000017_full_v1_curriculum_catalog.sql:1-11` 的 `source_type` CHECK 正是 `INTERNAL_PRODUCT_SPEC`、`OFFICIAL_STANDARD`、`TEXTBOOK_MAPPING`、`OPEN_LICENSE`；表含 `attribution`、`source_uri`、`version`、`status`。

### B. 当前来源行

**相符。** `000017_full_v1_curriculum_catalog.sql:23-25` 只有一条种子来源“互动式学习 V1 产品课程骨架”，类型 `INTERNAL_PRODUCT_SPEC`，备注明确“不代表教育部官方课程标准或教材版本”，状态 `RELEASED`。

### C. `knowledge_points` 维度

**相符。** `000002_curriculum.sql:48-67` 有 `curriculum_version`，没有地区或教材版本列。

### D. `knowledge_point_sources`

**相符。** `000017_full_v1_curriculum_catalog.sql:14-20` 主键为 `(knowledge_point_id, curriculum_source_id, source_ref)`；`basis_kind` CHECK 仅允许 `V1_SKELETON`、`TASK_019_DETAIL`。

### E. `world_connections`

**相符。** `000007_demo_curriculum.sql:46-60` 已有该表，连接类型为 `DAILY_LIFE`、`HUMAN_WORLD`、`SCIENCE_OR_CAREER`，且 `(knowledge_point_id, connection_type)` 唯一。静态种子为 15 个 `30000000-*` 演示知识点各 3 条连接，即 45 条；全量迁移后的实际数据库统计必须由负责人提供的 PostgreSQL 会话执行：

```sql
SELECT count(*) AS knowledge_points,
       count(*) FILTER (WHERE connection_count = 3) AS exactly_three,
       count(*) FILTER (WHERE connection_count < 3) AS gap_points,
       sum(connection_count) AS connection_rows
FROM (
  SELECT k.id, count(w.id) AS connection_count
  FROM knowledge_points k LEFT JOIN world_connections w ON w.knowledge_point_id = k.id
  GROUP BY k.id
) x;
```

基于迁移可确认的库存事实：15 个演示点有 3 条；其余由 `000017` 生成的 127 个点没有 `world_connections` INSERT，故静态缺口至少 127 个点、381 条连接。该“至少”避免把未提供的生产数据库状态冒充为静态迁移事实。

### F. 已发布知识点学科/学段分布

**不符（令文的“当前 127 个内部知识点”不是完整库存）。** 静态迁移实际生成 127 个 `v1-full-catalog` 点，另保留 15 个 `30000000-*` 演示点并迁入同一课程版本，共 142 个 `RELEASED` 点。学科/学段如下（127 + 15）：

| 学科 | 小学 | 初中 | 合计 |
|---|---:|---:|---:|
| 数学 | 22 | 29（26 + 3 演示） | 51 |
| 语文 | 12 | 15（12 + 3 演示） | 27 |
| 英语 | 7 | 13（10 + 3 演示） | 20 |
| 物理 | 0 | 23（20 + 3 演示） | 23 |
| 化学 | 0 | 21（18 + 3 演示） | 21 |
| **合计** | **41** | **101** | **142** |

## 5. 骨架差距分析

由于官方教材版本/册次尚未取得，不能合法或可靠地声称某个 code 已对应、缺失或超前于广州教材。本节因此给出可审计的“当前内部候选集合”，所有对应关系状态均为 `UNVERIFIED`；负责人提供征订单和章节目录后，才可把候选集合拆成四类差距。

- 数学（小学 22）：`MATH-PRI-NUMBER-PLACE-VALUE`, `MATH-PRI-FOUR-OPERATIONS`, `MATH-PRI-OPERATION-ORDER`, `MATH-PRI-DECIMALS`, `MATH-PRI-FRACTIONS`, `MATH-PRI-PERCENTAGES`, `MATH-PRI-FACTORS-MULTIPLES`, `MATH-PRI-RATIO`, `MATH-PRI-PROPORTION`, `MATH-PRI-SIMPLE-EQUATION`, `MATH-PRI-UNIT-CONVERSION`, `MATH-PRI-LENGTH-TIME-MASS`, `MATH-PRI-PERIMETER`, `MATH-PRI-AREA`, `MATH-PRI-VOLUME`, `MATH-PRI-ANGLE`, `MATH-PRI-PLANE-SHAPES`, `MATH-PRI-POSITION-DIRECTION`, `MATH-PRI-AVERAGE`, `MATH-PRI-STATISTICS-CHARTS`, `MATH-PRI-PROBABILITY-INTUITION`, `MATH-PRI-WORD-PROBLEM-MODELING`；初中为 `MATH-JUN-*` 全部 26 个种子 code 加 `MATH-LINEAR-EQUATION`、`MATH-FRACTION-COMMON-DENOMINATOR`、`MATH-PERCENT-DISCOUNT` 三个演示 code。
- 语文（小学 12、初中 12 种子 + 3 演示）：当前 code 集合为 `CHINESE-PRI-*`、`CHINESE-JUN-*` 以及 `CHINESE-INFORMATION-EXTRACTION`、`CHINESE-EVIDENCE-LOCATION`、`CHINESE-POETRY-IMAGERY`。
- 英语（小学 7、初中 10 种子 + 3 演示）：当前 code 集合为 `ENGLISH-PRI-*`、`ENGLISH-JUN-*` 以及 `ENGLISH-THERE-IS-ARE`、`ENGLISH-SIMPLE-PRESENT`、`ENGLISH-READING-DETAIL`。
- 物理（初中 20 种子 + 3 演示）：当前 code 集合为 `PHYSICS-*` 以及 `PHYSICS-SPEED`、`PHYSICS-DENSITY`、`PHYSICS-FORCE`。
- 化学（初中 18 种子 + 3 演示）：当前 code 集合为 `CHEMISTRY-*` 以及 `CHEMISTRY-CHANGE-TYPES`、`CHEMISTRY-AIR-OXYGEN`、`CHEMISTRY-ELEMENT-SYMBOL`。

在版本确认后，交付表必须逐 code 填写：直接对应、需要拆分/合并、教材有而项目缺、项目有而教材该学段不涉及；本期不填充推测结果。

## 6. 教材版本维度提案（不实施）

| 方案 | 查询与影响 | 发布门禁/课堂影响 | 142 个已发布内容影响 |
|---|---|---|---|
| 一：不改 `knowledge_points`，增加 `TEXTBOOK_MAPPING` 来源行并扩展 `basis_kind` | 查询通过 source join 过滤地区、教材、版本；旧查询默认仍读 V1 骨架 | 发布前检查 question/content version 的 source chain 是否覆盖目标教材映射；四阶段课堂继续绑定同一 knowledge point | 不迁移点 ID；现有 142 点保持可用，但需为要声称广州对齐的点补映射来源，且扩展枚举需前向 migration |
| 二：给 `knowledge_points` 增加地区/教材版本维度 | 查询直接过滤列，唯一性和索引更直观；同一概念可能出现多行或版本生命周期复杂 | 发布和四阶段运行时必须把地区/版本作为内容选择条件；DRAFT/RETIRED 版本不得进入课堂 | 需要前向 migration 和回填策略；现有内容必须明确 `GLOBAL/UNVERIFIED` 或映射到具体版本，错误回填会改变运行时选择 |

**推荐方案一。** 现有模型已经将课程骨架与来源分开，映射是来源事实而非新知识点语义；它对 142 个已发布内容的破坏性最小。只有在多个地区教材版本需要独立的知识点定义、难度或依赖关系时，才升级方案二。两案均未实施。

## 7. 自产题目与生活连接可行性

自产题目走现有内容流水线：人工/受控生成输入 → `DRAFT` → 确定性 Validator → 独立 Secondary Review → 记录 provenance/source → Owner 发布为 `RELEASED` → 隔离/撤回和审计历史。每题通过 `content_versions.source_id` 关联内容来源，并通过知识点的 `knowledge_point_sources` 关联 `curriculum_source`；广州教材映射未获负责人确认前，不得写入 `TEXTBOOK_MAPPING` 或宣称对齐。

按规格 18.2，每个知识点至少三种连接（生活、世界、人文/科学等）。静态迁移已为 15 个演示点提供三类连接；127 个课程目录点至少缺 381 条连接。以下是人工脱敏样例，样本量 `n=3`，不是流水线产出，也不能外推质量或成本：

```json
[
  {
    "knowledge_code": "MATH-LINEAR-EQUATION",
    "question_public": "三张同价门票加6元服务费共36元。你会先怎样表示每张门票的价格？",
    "connections": [
      {"type":"DAILY_LIFE","title":"分摊费用","explanation":"区分固定服务费与按人数变化的费用。"},
      {"type":"HUMAN_WORLD","title":"比较计费方案","explanation":"用同一关系比较不同收费规则。"},
      {"type":"SCIENCE_OR_CAREER","title":"建立模型","explanation":"把文字条件转成可检验的数量关系。"}
    ],
    "source": {"curriculum_source":"INTERNAL_PRODUCT_SPEC","source_ref":"manual-sample-1","status":"DRAFT"}
  },
  {
    "knowledge_code": "CHINESE-EVIDENCE-LOCATION",
    "question_public": "短文说‘小林把伞递给没带雨具的同学’。哪处文字能支持判断？",
    "connections": [
      {"type":"DAILY_LIFE","title":"澄清误会","explanation":"先找对方实际说过或做过的事。"},
      {"type":"HUMAN_WORLD","title":"新闻证据","explanation":"观点需要回到可核验的文字证据。"},
      {"type":"SCIENCE_OR_CAREER","title":"论证","explanation":"结论与证据之间要保持可追溯关系。"}
    ],
    "source": {"curriculum_source":"INTERNAL_PRODUCT_SPEC","source_ref":"manual-sample-2","status":"DRAFT"}
  },
  {
    "knowledge_code": "PHYSICS-SPEED",
    "question_public": "自行车在2小时内行驶30千米。要比较它每小时行多远，你会用哪两个量？",
    "connections": [
      {"type":"DAILY_LIFE","title":"规划出行","explanation":"用路程和时间比较出行快慢。"},
      {"type":"HUMAN_WORLD","title":"交通安全","explanation":"统一速度表达有助于理解道路信息。"},
      {"type":"SCIENCE_OR_CAREER","title":"工程测量","explanation":"测量量之间的比值支持实验和设计。"}
    ],
    "source": {"curriculum_source":"INTERNAL_PRODUCT_SPEC","source_ref":"manual-sample-3","status":"DRAFT"}
  }
]
```

规模估算（仅量级）：142 个知识点 × 3 条连接 = 426 条连接；若每点先产 3 道题，则约 426 道题，若每题经历一次 Validator、一次独立 Review，则约 852 次审核边界操作。模型成本只能在负责人批准 provider、model、价格目录后按实际 token 量估算；本期不调用生成模型、不填写厂商报价，也不把人工样例当作成本样本。

## 8. 证据分级

| 级别 | 本报告内容 | 边界 |
|---|---|---|
| 公开官方文件 | 广州 2025 课程计划、广州中考实施方案 | 证明政策和科目范围，不证明教材版本或授权教材正文 |
| 项目内部事实 | SQL 表结构、142 点库存、学科/学段计数、15 点 × 3 连接静态种子 | 证明仓库/迁移行为，不证明广州教材对齐 |
| 待项目负责人确认 | 每科教材出版社/版本/册次、学校适用范围、授权材料、实际数据库连接统计 | 未取得前不得导入或发布教材映射 |
| `UNVERIFIED` | 逐科教材版本、逐 code 四类差距、实际全库连接缺口、全量自产质量/成本 | 保持阻断，不以常识、教辅或百科补全 |

## 9. 未完成项与阻塞项

- 阻断：负责人尚未提供广州教育局/学校征订单或明确授权的教材版本清单。
- 阻断：未建立可审计的教材映射来源，不能声称广州对齐或导入教材内容。
- 待办：负责人提供隔离 PostgreSQL 后，执行 E 节统计 SQL 并把实际结果补入报告。
- 待办：版本确认后逐 code 完成四类差距矩阵；本期不凭推测填写。
- 未实施：任何 migration、业务代码、检索、批量模型产题、课堂运行时改动均未发生。

## 10. 主动提出但未实施的提案

1. 采用方案一，新增受控 `TEXTBOOK_MAPPING` 来源记录并扩展 `basis_kind`，保持现有 142 点 ID 稳定。
2. 为 `world_connections` 增加内容流水线的三连接完整性报告，在发布门禁中阻止缺连接知识点进入目标教材版本。
3. 在获得授权清单后，为每个版本生成“映射差距矩阵”和可撤回的来源快照；不复制教材正文。

## 11. 本期改动清单

- `docs/architecture/guangzhou-curriculum-alignment-spike.md:1`：新增本 P0 取证报告；记录官方来源、A-F 复核、库存差距、授权阻断、方案比较和人工样例。
- 未修改 Go、SQL、Vue、Schema、部署配置或运行时课堂代码。

## 12. 门禁原始输出

以下为 2026-09-17 在本工作区执行的终端输出。未配置真实 PostgreSQL，因此集成安全门主动失败；这不是“通过”或“跳过”。

### `go vet ./server/... ./schemas/...`

```text
(无输出，退出码 0)
```

### `go test -count=1 ./server/internal/... ./server/cmd/... ./schemas/...`

```text
ok  github.com/oppositenum/ai-learning-tutor/server/internal/ai 1.058s
ok  github.com/oppositenum/ai-learning-tutor/server/internal/api 1.375s
ok  github.com/oppositenum/ai-learning-tutor/server/internal/auth 4.364s
ok  github.com/oppositenum/ai-learning-tutor/server/internal/classroom 2.695s
ok  github.com/oppositenum/ai-learning-tutor/server/internal/content 2.236s
ok  github.com/oppositenum/ai-learning-tutor/server/internal/contentpipeline 3.134s
ok  github.com/oppositenum/ai-learning-tutor/server/internal/curriculum 1.804s
?   github.com/oppositenum/ai-learning-tutor/server/internal/database [no test files]
?   github.com/oppositenum/ai-learning-tutor/server/internal/health [no test files]
ok  github.com/oppositenum/ai-learning-tutor/server/internal/mastery 3.551s
ok  github.com/oppositenum/ai-learning-tutor/server/internal/parent 3.572s
ok  github.com/oppositenum/ai-learning-tutor/server/internal/planner 3.742s
ok  github.com/oppositenum/ai-learning-tutor/server/internal/realtime 3.844s
ok  github.com/oppositenum/ai-learning-tutor/server/internal/reward 4.053s
ok  github.com/oppositenum/ai-learning-tutor/server/internal/safety 3.878s
?   github.com/oppositenum/ai-learning-tutor/server/internal/seedcontent [no test files]
ok  github.com/oppositenum/ai-learning-tutor/server/internal/speech 3.661s
ok  github.com/oppositenum/ai-learning-tutor/server/internal/student 3.671s
ok  github.com/oppositenum/ai-learning-tutor/server/internal/studentinteraction 3.515s
?   github.com/oppositenum/ai-learning-tutor/server/internal/subject [no test files]
?   github.com/oppositenum/ai-learning-tutor/server/internal/trial [no test files]
ok  github.com/oppositenum/ai-learning-tutor/server/internal/tutor 3.344s
ok  github.com/oppositenum/ai-learning-tutor/server/internal/tutoraudit 3.356s
ok  github.com/oppositenum/ai-learning-tutor/server/internal/usage 3.405s
?   github.com/oppositenum/ai-learning-tutor/server/internal/user [no test files]
ok  github.com/oppositenum/ai-learning-tutor/server/cmd/api 3.364s
?   github.com/oppositenum/ai-learning-tutor/server/cmd/migrate [no test files]
ok  github.com/oppositenum/ai-learning-tutor/server/cmd/provider-compat-probe 3.432s
?   github.com/oppositenum/ai-learning-tutor/server/cmd/provision [no test files]
?   github.com/oppositenum/ai-learning-tutor/server/cmd/seed-linear-equation [no test files]
ok  github.com/oppositenum/ai-learning-tutor/schemas/ai_outputs 3.494s
```

### `CI=1 go test -count=1 ./server/tests/integration`

```text
--- FAIL: TestIntegrationDatabaseConfiguredInCI (0.00s)
    security_gate_test.go:34: TEST_DATABASE_URL must be configured in CI so PostgreSQL safety tests cannot silently skip
FAIL
FAIL github.com/oppositenum/ai-learning-tutor/server/tests/integration 4.523s
FAIL
```

顶层计数：`PASS=0 FAIL=1 SKIP=0`（未进入数据库测试；安全门先失败）。

### `npm test && npm run lint && npm run build`

```text
Test Files 20 passed (20)
Tests 113 passed (113)
Duration 4.70s
eslint . --max-warnings 0   (退出码 0)
vite v8.2.2 building client environment for production...
✓ 1873 modules transformed.
✓ built in 411ms
```

退出码 0。

### `npm run test:e2e`

```text
> ai-learning-tutor@0.1.0 test:e2e
> playwright test

[WebServer] (node:50839) Warning: The 'NO_COLOR' env is ignored...
Error: Timed out waiting 120000ms from config.webServer.
```

退出码 1；没有产生可计入验收的 E2E 测试结果。

## 13. 结论

广州教材版本对齐目前是授权和官方版本清单阻断的取证问题，不是可以靠猜测补齐的课程代码问题。项目可立即推进的是内部原创题目与连接的离线方案设计；在负责人提供版本和授权证据前，保持 `UNVERIFIED`、不导入、不启动 P1。

Thu Sep 17 14:33:38 CST 2026
