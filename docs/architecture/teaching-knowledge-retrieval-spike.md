# 教学知识检索（RAG）决策级预研与内容流水线侧落地报告

## 1. 基线与范围

- 基线分支：`feat/provider-identity-and-prompts`
- 起始 SHA：`5d4d3a653f4f5c6e6c9703938d13d5ad9386ff0a`
- 结束 SHA：`5d4d3a653f4f5c6e6c9703938d13d5ad9386ff0a`（报告提交前的代码基线；本期无业务代码改动）
- 范围：P0 预研取证；本期没有实现检索接口、适配器、迁移或课堂运行时接入。
- 查阅日期：2026-09-17（Asia/Shanghai）。

### 改动清单

- `docs/architecture/teaching-knowledge-retrieval-spike.md:1`：新增本期 P0 预研报告，记录 A-F 复核、官方取证、计价阻断、授权边界、成本样本、运行时独立判断、门禁结果和未实施提案。
- 未修改任何 Go、SQL、Vue、Schema、部署配置或运行时课堂代码。

本期不上课堂运行时。仓库已有一条实测阻断性冲突：`PROBE`、`HINT`、
`SCAFFOLD`、`ANALOGY` 的数字材料必须来自原题，而 `questionMaterialText`
只读取题面和场景标签，不读取检索结果。若把教材片段直接喂给运行时生成器，
教材中的数字会扩大确定性材料门禁的拒绝面；因此本期只研究内容流水线的离线
生成期检索。该边界不是提示词约束，而是服务端可执行门禁的现实约束。

## 2. A-F 事实复核

### A. 数字材料门禁：相符

`server/internal/ai/material_policy.go` 的 `enforceTutorMaterialPolicy` 对
`PROBE`、`HINT`、`SCAFFOLD`、`ANALOGY` 调用 `numericMaterial`，允许集合来自
`questionMaterialText`。后者只拼接 `QuestionPublic.Prompt`、解析后的场景
`AccessibleFallback`、选项/项目/左右项/分组/槽位和数轴范围、步长；没有检索
参数或检索文本。`EXPLAIN`/`VOICE_EXPLAIN` 才是不同数字平行例外。仓库现有
`server/tests/integration/tutor_live_provider_test.go` 也将引入题面外数字的
HINT 作为预期 fail-closed 边界。令文给出的 2026-09-16 数学题 6 次生成中
5 次被拒绝的实测数字未在当前仓库日志中找到，不能把它扩大成新的仓库事实；
但代码级冲突相符，足以阻断运行时接入。

### B. 价格字段：相符

`server/migrations/000005_adaptive_engines.sql` 的 `ai_price_catalog` 只有
`input_price_per_million_usd`、`cached_input_price_per_million_usd`、
`output_price_per_million_usd`、音频输入/输出每分钟价格；
`000011_ai_cost_tier.sql` 只增加 `cost_tier`。没有按次查询、索引构建或存储
计价字段。

### C. 价格预检：相符

`server/internal/usage/recorder.go` 的 `EnsurePrice` 找不到当前生效的
`(provider, model)` 行即返回 `ErrPriceNotFound`。调用方在联网前执行预检，
因此未完成价格方案前不得让任何知识检索调用绕过记录。

### D. purpose 约束：相符

`000005` 的 `ai_usage_records.purpose` 是 `text NOT NULL`，无 CHECK；
`000030` 的 `ai_request_outcomes.purpose` 才有长度约束。新增检索用途若落在
`ai_usage_records`，必须使用稳定、长度受控的字符串；若记录到请求结果表，
则还需满足其长度 CHECK。

### E. 内容溯源：相符

`000017_full_v1_curriculum_catalog.sql` 已有
`knowledge_point_sources(knowledge_point_id, curriculum_source_id, source_ref,
basis_kind)`，`basis_kind` 仅允许 `V1_SKELETON`、`TASK_019_DETAIL`。它适合
记录项目课程来源，但不能直接承载第三方知识库的 provider/index/document/
chunk 身份；后续应复用项目来源主链，并新增受控的外部索引映射，而不是把云端
ID塞进 `source_ref` 后失去结构化约束。

### F. 知识库代码库存：相符

排除 `knowledge_point` 后，`knowledgebase`、`workspace_id`、`retriev`、
`rerank`、`top_k` 在仓库无命中。当前没有知识库客户端、同步器、检索日志或
workspace/index 配置。

## 3. 官方厂商取证

### 3.1 千问 / 百炼

- **地域与账号**：百炼知识库 API 指南写明知识库相关功能仅能在中国站华北2
  （北京）地域开通和使用；子账号需 `AliyunBailianDataFullAccess`，并加入
  业务空间，调用需要 AccessKey、AccessKey Secret、`WORKSPACE_ID`。还需要
  知识库/index 标识和文档资源标识。[官方 API 指南](https://www.alibabacloud.com/help/zh/model-studio/rag-knowledge-base-api-guide)
- **能力**：Knowledge Studio API 可管理知识库、导入文档、执行检索和知识问答；
  文档搜索与结构化数据查询可分别承载教材文本和知识点元数据。[官方 API 概览](https://docs.agent.bailian.aliyun.com/zh/api/rag/overview)
- **计费**：官方知识库说明写明知识库构建本身不收费，仅调用 `Retrieve` 不经过
  百炼应用生成回答时不产生费用；若通过应用生成回答，召回切片会增加模型输入
  Token 并增加模型推理费用。[官方知识库计费说明](https://docs.modelstudio.console.alibabacloud.com/zh/model-studio/rag-knowledge-base)
  知识库功能本身免费，构建、管理运维和纯 `Retrieve` 不收费；选择 ADB-PG
  向量存储可能产生费用，应单独评估，不推广到默认知识库路径。
- **结论**：百炼纯检索按上述官方口径免费；召回文本引入的生成成本就是模型
  输入 Token，现有 `ai_price_catalog` 可以表达。因此百炼当前不需要新增计价
  维度，计价建模不构成其 P1 阻断。免费检索仍须记录调用、零成本依据与归因，
  后续模型调用仍必须走现有价格预检和用量记录。

### 3.2 豆包 / 火山方舟

- **地域与账号**：方舟知识库流程要求在火山方舟控制台创建知识库、导入文档并在
  处理完成后检索；API 对接需要完成账号实名认证、AK/SK 和签名获取。知识库
  插件可通过 Bot ChatCompletions/Bot SDK 调用，实际接入还需要方舟区域、项目/主
  账号权限和知识库标识。[官方文档知识库核心流程](https://www.volcengine.com/docs/82379/1261883?lang=zh)
- **能力**：支持非结构化和结构化文档、混合检索、重排、标签过滤、切片溯源，
  并明确覆盖教育搜题解题场景。[官方知识库产品页](https://www.volcengine.com/product/KnowledgeBase)
- **计费**：官方资料把知识库按规格、检索次数/配额、模型调用等维度区分；免费
  知识库说明包含 30 天、300 次/天检索和 100 万 Token 模型额度，升级标准版后
  按标准版计费。[官方免费知识库说明](https://www.volcengine.com/docs/82379/1904605?lang=zh)
  方舟文档还明确提示知识库召回内容会作为模型输入，从而增加 Token 消耗；知识库
  文档处理、存储/规格和模型调用不能压缩为一条 Token 单价。[官方知识库插件说明](https://www.volcengine.com/docs/82379/1528458?lang=zh)
- **结论**：方舟纯检索和知识问答的具体生产价格需要按账号、区域、知识库规格和
  控制台账单确认；当前公开资料不足以生成可安全写入生产目录的统一美元单价。

以上均为官方文档能力/计费说明，不是本项目的实时调用证据。当前没有向任一厂商
发送请求，也没有读取或保存密钥、workspace、index 或知识库内容。

## 4. 计价与记账决策（百炼不阻断，方舟阻断）

百炼默认纯 `Retrieve` 路径免费，召回新增模型输入 Token 可由现有目录表达，
不需要为了该路径新增价格维度。调用归因与零成本检索记录仍需在 P1 设计，记录
结构若需持久化变更仍走前向 migration；这不等同于新增收费维度。

方舟价格证据仍不足。现有目录只能表达模型 Token 和音频分钟，不能表达：

- 按检索次数或套餐配额；
- 知识库规格时长、索引构建、文档处理或存储；
- 云厂商按小时生成的知识库账单；
- 一次检索关联多个召回 chunk 的外部账单明细。

**最小 migration 提案（未实施，仅适用于方舟等独立收费路径）**：新增前向 migration，建立独立的
`knowledge_retrieval_price_catalog`（provider、knowledge_base_id、region、
pricing_unit、unit_price_usd、effective_from/to、source_ref），以及
`knowledge_retrieval_records`（request_id、provider、knowledge_base_id、
document_id、chunk_id、query_kind、top_k、latency_ms、adopted、model_request_id、
price_catalog_id、estimated_cost_usd、created_at）。按次/按小时/按 Token
使用不同 `pricing_unit`，不要污染 `ai_price_catalog` 的模型字段语义。所有
检索请求在联网前也执行 fail-closed 价格预检。

**不迁移的替代方案（方舟收费路径，不批准）**：检索不进 AI 用量，只另记无成本的审计事件，
由云账单人工对账。后果是 Owner 成本报表、单位知识点成本和预算告警低估或无法
解释，不能满足“每次 AI 请求有适用价格版本”的可审计要求，也无法在发布前阻止
未计价检索。

百炼免费检索必须保留官方零成本依据与检索记录，不得把后续模型调用漏记；模型
调用不得绕过 `EnsurePrice`。方舟收费路径在价格方案裁决前不得联网调用。

## 5. 授权与合规（阻断项）

当前项目可证明合法使用的内容范围仅为：

| 来源 | 依据 | 处理结论 |
| --- | --- | --- |
| `docs/product/互动式学习_V1.md` 课程骨架 | `curriculum_sources.source_type=INTERNAL_PRODUCT_SPEC`，版本 V1.0，项目自有文档 | 可作为内部课程元数据；仍需经过项目 RELEASED 门禁 |
| 项目自有生成内容和测试夹具 | 仓库自身内容 | 仅限脱敏、非学生私有数据；生成结果先入 DRAFT |
| 明确标注 `OFFICIAL_STANDARD` 的官方标准 | 需在 `curriculum_sources` 中登记官方 URL、版本、授权/公开依据 | 只有登记并审核后可导入 |
| 明确标注 `OPEN_LICENSE` 且满足许可证条件的材料 | 需登记许可证、归属和 source URI | 只有登记、归属和审核后可导入 |

没有看到已登记的第三方教材、商业题库或厂商教学语料授权凭证。因此本期明确
排除：商业教材扫描件、商业题库/答案、爬取的题库、未授权课程讲义、学生答案、
学生语音、课堂 transcript、私有答案字段和任何原始儿童音频。厂商知识库服务
不会替我们取得这些资料的版权。

## 6. 脱敏成本量级样本

本节是离线估算，不是厂商账单或真实检索结果。样本为 3 条人工编写、无学生数据、
无答案字段的教学片段，分别约 118、146、171 个汉字；按保守的 4 个字符约 1
Token 估算，TopK=3 时拼接后的输入增加约 108、132、157 Token，均值约 132
Token。样本量 `n=3`，不能外推生产分布。

成本变化公式为：

```text
增量模型成本 = 召回新增输入 token / 1,000,000 × 生效 input 单价
```

例如仅为展示数量级，若目录测试行的 input 价格为 `$1.00/M`（仓库测试夹具值，
不是豆包或千问生产报价），132 Token 的单次增量是 `$0.000132`；若为 `$0.10/M`
则是 `$0.0000132`。这不能替代两家按区域、模型、知识库规格和账单周期提供的
真实报价。百炼纯 `Retrieve` 按官方口径免费，召回引入的模型输入成本由现有
目录表达，不需要新增知识库计价维度。方舟的独立检索/规格费用仍待核实，不得
直接套用上述模型 Token 成本公式。

## 7. 运行时可行性独立判断

在不修改 `material_policy.go`、`questionMaterialText` 和 numeric material 的
前提下，**直接把检索片段喂给五个运行时教学动作不可行**：片段中的数字不属于
原题允许集合，确定性门禁会拒绝；而“提示模型不要使用检索中的数字”只是提示词
约束，不是可执行约束。

一个可行但未批准的**提案**是“运行时检索只服务 `EXPLAIN`/`VOICE_EXPLAIN` 的
平行示例”：服务端先把检索片段转成不含数值的概念证据，或将其作为审核器私有的
事实参考，生成器仍只能使用题面数字；随后独立审核、数字门禁和原题回访保持不变。
该提案需要额外的可执行脱数字化转换器、来源/版本审计、失败重试边界和专项集成
测试，影响 Tutor Gateway、审核器和延迟/成本；不做这些工作就会放大拒绝率或引入
答案泄露风险。因此本期不实施，也不把它描述成已解决路径。

## 8. 证据分级

| 证据类别 | 本报告内容 | 边界 |
| --- | --- | --- |
| Mock 证据 | 仓库单元测试、价格预检测试、结构化输出兼容性测试 | 只证明本地代码行为，不证明厂商能力或价格 |
| 真实厂商证据 | 当前仓库 2026-09-16 豆包/千问结构化输出探针小样本；官方文档的知识库能力和计费说明 | 探针只描述该次运行；官方文档不是本项目实时调用证据 |
| 测试环境体验 | 本期没有知识库测试环境调用或发布体验 | 不得用模型探针替代知识库体验 |
| UNVERIFIED | 两家生产知识库价格明细、稳定性、召回质量、授权内容覆盖、同步删除语义 | 在取得账号/区域/知识库和账单证据前保持未验证 |

## 9. 未完成项与阻塞项

- 未取得项目方实际的百炼 `WORKSPACE_ID`、index/knowledge-base ID、北京地域权限、
  子账号策略；未取得方舟 region/project/知识库规格和 AK/SK 配置。
- 方舟独立知识库价格明细仍未取得，构成方舟适配器阻断；百炼默认免费检索
  不受此阻断，后续模型仍需有效价格行。ADB-PG 收费存储不纳入当前先行方案。
- 未建立外部文档授权清单，不能导入第三方教材或商业题库。
- 未实现检索接口、适配器、同步器、检索记录表或 migration，符合本期不写业务代码
  的范围。
- 运行时检索仍受 A 项确定性材料门禁阻断，课堂路径保持不接入。

## 10. 主动提出但未实施的提案

1. **方舟独立检索价格目录与记录表**：解决现有 token-only 目录无法表达收费知识库账单
   的问题；影响新增 migration、Owner 报表和发布前预检；不做会导致成本不可审计。
2. **内容流水线统一检索接口 + 单一适配器**：P0 裁决后只接一家，将 provider、
   knowledge base、document/chunk、TopK、latency、adopted 和模型 request ID
   固化为服务端 DTO；影响内容生成路径和测试；不做会继续出现供应商 SDK 直接耦合。
3. **RELEASED-only 同步器**：DRAFT 不入索引，撤回/隔离/版本变更可停用 chunk，
   同步失败 fail-closed 可重试；影响内容发布事务和外部索引状态表；不做会造成
   运行时读到未发布或已撤回材料。
4. **运行时 EXPLAIN 专项提案**：仅在可执行数字脱敏、独立审核和专项测试完成后
   评估；影响 Tutor Gateway、审核和延迟预算；本期不做，避免把提示词当安全边界。

## 11. 门禁原始输出

本期仅新增文档，仍按令文执行门禁。以下为本次命令的实际输出摘要；没有把 CI 结果
冒充本地结果，也没有把文档测试冒充厂商知识库体验。

```text
`go vet ./server/... ./schemas/...`
```text
exit=0
```

`go test -count=1 ./server/internal/... ./server/cmd/... ./schemas/...`
```text
exit=0
ok github.com/oppositenum/ai-learning-tutor/server/cmd/api
ok github.com/oppositenum/ai-learning-tutor/server/cmd/provider-compat-probe
ok github.com/oppositenum/ai-learning-tutor/schemas/ai_outputs
```

`CI=1 TEST_DATABASE_URL='postgres://a0000@127.0.0.1:5432/learning_tutor_rag_spike?sslmode=disable' go test -count=1 ./server/tests/integration`
```text
ok github.com/oppositenum/ai-learning-tutor/server/tests/integration 68.970s
TOP_LEVEL PASS=125 FAIL=0 SKIP=0
TestIntegrationDatabaseConfiguredInCI: PASS
```

审计纠正：顶层只统计测试名不含 `/` 的测试，排除子测试和包级 PASS。
原报告的 147 是错误计数（146 个含子测试 PASS 加 1 个包级 PASS），不是顶层
基线。正确计数为 125；FAIL=0、SKIP=0 不变，门禁实质通过。此处计数为审计复核
结果，不伪称原命令直接输出了正确计数。

`npm test && npm run lint && npm run build`
```text
Test Files 20 passed (20)
Tests 113 passed (113)
eslint . --max-warnings 0: exit=0
vite build: exit=0
```

`NO_PROXY=127.0.0.1,localhost no_proxy=127.0.0.1,localhost npm run test:e2e`
```text
Running 1 test using 1 worker
1 passed (1.6s)
```
```

集成测试门槛：顶层 PASS >= 125、FAIL = 0、SKIP = 0，且
`TestIntegrationDatabaseConfiguredInCI` 已通过；集成测试使用本机隔离 PostgreSQL
数据库 `learning_tutor_rag_spike`，不是 CI 数据库，也不代表远端 CI 已运行。

## 12. 结论

P0 建议：**不接课堂运行时；P1 只按千问/百炼单家推进，方舟留接口不实现。**
百炼默认免费检索不构成计价建模阻断，方舟仍受独立计价证据阻断。内容授权清单
是百炼先行路径剩余的决策阻断；实际执行还需华北2（北京）的 workspace ID、
index ID、子账号权限、脱敏样本和有效模型价格行。P1 尚未实施，需项目负责人
裁决；免费检索记录和后续模型价格预检、用量归因均不得省略。

落款：
```text
Thu Sep 17 01:43:35 CST 2026
```
