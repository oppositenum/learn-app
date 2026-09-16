# 豆包 / 千问 Teaching Agent Provider 对接交接说明

本文面向首次接触本仓库的外部工程团队，说明如何在不破坏既有教学、安全、计费和发布边界的前提下，为系统接入豆包（Doubao）与千问（Qwen）模型 Provider。这里的 **Provider** 是把具体模型厂商的 SDK 或 HTTP API 适配为本项目 `TeachingAgent` 接口的服务端实现。

## 1. 项目概览

AI Learning Tutor 是面向小学与初中学生的五科苏格拉底式 AI 学习平台，统一支持数学、语文、英语、物理和化学。产品包含学生端、家长端和管理端（代码中的角色名为 `OWNER`）：学生端负责学习与课堂交互，家长端查看经过权限裁剪的学习状态并提供支持，管理端负责内容、成本和学习效果治理。当前系统是模块化单体架构，前端使用 Vue，服务端使用 Go，持久化使用 PostgreSQL，课堂实时事件使用 WebSocket；学生浏览器只访问本项目 Go API。

产品与验收的唯一当前基线是 `docs/product/互动式学习_V1.md` 的 V1.1 修订版。仓库级不可违反规则位于根目录 `AGENTS.md`。

## 2. 当前代码与发布状态

本说明编写时的状态如下：

这里的 SHA 是 Git commit 的完整 40 位标识；CI 是由 GitHub Actions 执行的持续集成检查。

- 开发分支 `feat/v1-roadmap-completion` 位于 `03f4cc987d1cf229aba961dcfafaed4078aabe71`，该提交已推送到远端。
- `main` 位于 `bc6d8d901af5be3dc0ce16644e9dacf42044d82f`，开发分支尚未合并到 `main`。
- `03f4cc987d1cf229aba961dcfafaed4078aabe71` 对应的 CI 当前为失败状态。失败 job 是 `PostgreSQL integration tests`，失败步骤是 `Test against real PostgreSQL`。
- 同一 SHA 的本地五条门禁均已通过；真实 PostgreSQL 集成测试的顶层统计为 `PASS=124 / FAIL=0 / SKIP=0`。本地通过不等于 CI 通过，CI 仍是远端环境的权威证据。
- 当前 CI 失败表现为课堂提交超时相关集成测试中的低概率、机器速度相关时序问题，不是豆包或千问 Provider 特有问题；根因尚未最终定位。
- 在同一 SHA 的 CI 全绿之前，该分支暂不可发布到测试服务器。

上述 CI 阻塞与 Provider 对接是两条不同工作线。Provider 开发应从双方确认的基线另开功能分支，不得在 `feat/v1-roadmap-completion` 上直接提交，也不得用 Provider 改动掩盖或绕过现有 CI 失败。

## 3. 代码地图

| 路径 | 职责 |
| --- | --- |
| `server/internal/ai/` | `TeachingAgent` 接口、结构化请求/响应类型、现有 `CodexProvider`、OpenAI Responses 客户端、重试策略、JSON Schema 校验和 Tutor 材料安全策略。 |
| `server/internal/classroom/` | 课堂提交、生命周期、苏格拉底状态、四阶段课堂、证据、实时事件和语音解释编排；通过 `WithTeachingAgent` 接收 Provider。 |
| `server/internal/contentpipeline/` | 教学内容的生成、确定性校验、独立审核、来源记录、发布和隔离。内容生成 Provider 与运行时 Tutor Provider 的职责不同，但共享独立审核和计费原则。 |
| `server/internal/usage/` | 价格预检、版本化价格查询、Token/音频用量计费和 `ai_usage_records` 写入。 |
| `apps/web/src/` | Vue 学生端、家长端和管理端页面、路由、状态管理、API 客户端和响应式交互。Provider 密钥和厂商 SDK 不得进入这里。 |
| `server/migrations/` | PostgreSQL 的前向数据库迁移（migration），是生产数据库结构契约；持久化字段变更必须通过新 migration 完成。 |
| `docs/` | 产品基线、API、架构、安全、质量验收和发布说明。关键入口包括 `docs/product/互动式学习_V1.md`、`docs/quality/v1-acceptance.md` 和 `docs/deployment/一键发布.md`。 |

应用启动时的现有接线位于 `server/cmd/api/main.go`：服务端创建客户端、用量记录器和独立审核器，构造 `TeachingAgent`，再通过 `classrooms.WithTeachingAgent(agent)` 注入课堂服务。新 Provider 最终也要在这一服务端组合根完成选择与注入，但具体配置形式由对接方案决定。

## 4. TeachingAgent 对接契约

`TeachingAgent` 定义在 `server/internal/ai/gateway.go`，共有五个方法：

```go
type TeachingAgent interface {
    AnalyzeAnswer(ctx context.Context, request AnalyzeAnswerRequest) (AnalyzeAnswerResult, error)
    GenerateTurn(ctx context.Context, request GenerateTurnRequest) (TutorTurn, error)
    GenerateAnalogy(ctx context.Context, request AnalogyRequest) (TutorTurn, error)
    GenerateParallelExample(ctx context.Context, request ExampleRequest) (TutorTurn, error)
    GenerateExplanation(ctx context.Context, request ExplainRequest) (Explanation, error)
}
```

它们的职责分别是：

- `AnalyzeAnswer`：分析学生作答并返回诊断、误概念、能力信号、情绪/参与度和建议动作。它只提供分析，不得直接修改掌握状态或学习计划。
- `GenerateTurn`：按服务端规则引擎已经授权的课堂动作生成一轮 Tutor 文本。
- `GenerateAnalogy`：生成生活类比，帮助理解，但不得泄露原题答案。
- `GenerateParallelExample`：用不同数值或不同材料生成平行示例，不得替学生解出原题。
- `GenerateExplanation`：生成简短解释或语音脚本；输出仍须经过答案非披露审核，并回到原任务验证。

请求、响应和用量类型定义在 `server/internal/ai/types.go`。生产代码目前只有 `server/internal/ai/codex_provider.go` 中的 `CodexProvider` 实现了这套接口。产品规格列出了 `ClaudeProvider` 和 `GeneralOpenAIProvider` 作为预留名称，但仓库中没有它们的实现，也没有豆包或千问实现。

接入新厂商时，需要完成以下契约工作：

1. 在服务端实现完整 `TeachingAgent` 五方法，或实现可被一个完整 `TeachingAgent` 适配器使用的厂商客户端；不能只实现当前演示路径碰巧调用的方法。
2. 把厂商响应转换为 `server/internal/ai/types.go` 中的类型，并保留服务端规则引擎对动作、掌握度和课堂状态的最终控制权。
3. 对每次调用提供真实的 `provider`、`model`、请求 ID 和用量数据，接入价格预检和 `UsageRecorder`。
4. 对结构化输出执行本地 JSON Schema 校验；不能仅信任厂商返回的“JSON mode”标志。
5. 对所有学生可见生成接入现有双重答案非披露审核，并让生成方与审核方身份可配置且可追溯。
6. 在 `server/cmd/api/main.go` 的服务端组合根中完成配置和课堂注入；浏览器端不参与 Provider 选择、鉴权或直接调用。
7. 为适配器、失败模式、计费、审核和课堂状态边界增加单元测试与真实 PostgreSQL 集成测试。

本说明不替对接团队决定是分别编写 `DoubaoProvider` / `QwenProvider`，还是抽取共享适配层，也不决定使用厂商 SDK 还是 HTTP 客户端。无论选择哪种结构，上述行为契约和下述硬约束都必须保持一致。

## 5. 会直接阻塞联调的硬约束

### 5.1 价格预检与版本化计费

**是什么：** 每个 Provider/Model 在发起网络请求前，都必须在 `ai_price_catalog` 中存在调用时间点有效的价格记录。现有 `OpenAIResponsesClient.GenerateStructured` 会先调用 `PriceGuard.EnsurePrice`，找不到价格就返回 `ErrPriceNotFound`，不会发出厂商请求。新 Provider 必须保持同样的 fail-closed 行为；fail closed 指依赖缺失或校验失败时拒绝继续，而不是降级放行。

**为什么：** 项目要求每次 AI、STT（语音识别）和 TTS（语音合成）调用都绑定实际使用的版本化价格条目，不能先调用、后补计费，也不能把价格硬编码在业务逻辑中。

**不满足会怎样：** Provider 在联网前即被拦截，无法联调；如果新适配器绕过预检，则违反成本和审计边界，不能合入。

`ai_price_catalog` 的实际字段由 `server/migrations/000005_adaptive_engines.sql` 和 `000011_ai_cost_tier.sql` 定义：

- `id`
- `provider`
- `model`
- `effective_from`
- `effective_to`
- `input_price_per_million_usd`
- `cached_input_price_per_million_usd`
- `output_price_per_million_usd`
- `audio_input_price_per_minute_usd`
- `audio_output_price_per_minute_usd`
- `created_at`
- `cost_tier`，取值为 `LOW`、`STANDARD` 或 `STRONG`

现有金额字段全部以美元计价，最终估算成本写入 `ai_usage_records.estimated_cost_usd`。豆包和千问通常以人民币报价，因此对接前必须明确选择：按受控汇率换算为美元后登记，或通过 migration 扩展币种及换算模型。这个选择会影响数据库、成本计算和管理端展示，应由项目负责人批准；本说明不替团队做该决策。

### 5.2 当前没有 cache-write 价格字段

**是什么：** 价格表有普通输入、缓存命中输入和输出价格，但没有缓存写入（cache write）价格字段。

**为什么：** 有些厂商会把缓存写入与缓存读取分别计费；现有模型只能表达 `cached_input_price_per_million_usd`，无法单独表达写入价格。

**不满足会怎样：** 如果豆包或千问对缓存写入单独收费，而适配器仍按现有字段记账，成本会被低估。不得把缓存写入价格塞进音频字段或其他无关字段。需要决定暂时禁用/不使用该计费能力，还是新增明确的 migration、计算和展示字段。

### 5.3 结构化输出必须通过严格 Schema

**是什么：** `AnalyzeAnswer` 使用 `schemas/ai_outputs/analyze_answer.schema.json`，学生可见 Tutor 输出使用 `schemas/ai_outputs/tutor_turn.schema.json`。现有实现使用 `santhosh-tekuri/jsonschema` 在本地再次校验厂商返回值，然后才反序列化为 Go 类型。相关审核输出也有独立 Schema。

**为什么：** 课堂规则依赖枚举、字段类型和完整性。自由格式 JSON、额外字段、缺失字段或模型自报的安全标志都不能成为业务依据。

**不满足会怎样：** 返回会被拒绝，课堂不会消费这次模型输出。对接前要验证两家厂商的 structured output 或 JSON mode 能否稳定生成符合这些 Schema 的结果；即使厂商宣称支持，也必须保留本地校验。

### 5.4 学生可见输出必须经过双闸门

**是什么：** 任何准备展示给学生的 Tutor 文本及其语音分段，在持久化、WebSocket 实时推送和 TTS 之前，必须同时经过：

1. 确定性答案比对；
2. 独立模型审核。

现有入口在 `server/internal/ai/codex_provider.go` 调用 `TutorOutputAuditor`，审核服务位于 `server/internal/tutoraudit/`。确定性闸门和独立审核都运行，任一拒绝、不可用、超时、结构错误、来源不符或审计记录无法持久化，输出都 fail closed，即不发布给学生。

**为什么：** 模型不能仅凭提示词保证不泄露答案。双闸门用于阻止直接答案、等价答案、完整解法以及分段中的答案泄露。

**不满足会怎样：** 学生可见内容不得落库、推送或合成为语音。新 Provider 不能绕过 `TutorOutputAuditor`，也不能在审核完成前自行调用 TTS 或实时通道。

### 5.5 生成方与审核方必须是不同身份

**是什么：** 生成方和审核方用 `provider:model` 字符串标识。`server/internal/tutoraudit/service.go` 和 `server/internal/contentpipeline/reviewer.go` 都会拒绝相同身份，并校验审核返回的实际 Provider、Model 与请求 ID。

**为什么：** 同一模型自审不能提供规格要求的独立证据；真实来源校验也防止配置名与实际调用模型不一致。

**不满足会怎样：** 审核服务无法构造或返回无效来源，学生输出/内容审核 fail closed。

同时接入豆包和千问提供了清晰的分工机会：可以由一家生成、另一家审核，并根据测试结果决定反向组合。这比当前依赖同一厂商下不同模型身份更符合独立审核目标，建议将双厂商分工作为对接设计的显式方案进行验证。这里不预设哪一家负责生成或审核。

### 5.6 提交路径总预算为 75 秒

**是什么：** `server/internal/classroom/lifecycle.go` 定义 `SubmitOverallTimeout = 75 * time.Second`。同文件定义反向代理读取预算 85 秒、陈旧会话自动暂停阈值 90 秒，形成 `75s < 85s < 90s` 的顺序；前端 `apps/web/src/lib/studentInteraction.ts` 同步镜像 75 秒提交预算，`deploy/nginx.conf` 使用 85 秒代理读写超时。

**为什么：** 服务端必须先结束请求，代理才能留出返回错误的时间；仍在处理的请求也不能被会话恢复逻辑提前回收。单元测试会同时校验服务端、前端和 Nginx 配置关系。

**不满足会怎样：** 单独调整任一数字会破坏跨层不变量并导致测试失败，也可能造成代理先断开或会话被过早回收。请把生成、审核、重试和计费落库都纳入 75 秒总预算，使用两家厂商的真实延迟分布做评估；如确需调整，必须整组重新论证和修改，不能只改 Provider 超时。

### 5.7 密钥与网络边界只在服务端

**是什么：** 学生浏览器只调用本项目 Go API，所有模型厂商密钥、签名和 SDK 调用都在服务端。

**为什么：** 浏览器密钥无法保密，也会绕过权限、价格、用量、审核、课堂状态和审计记录。

**不满足会怎样：** 方案违反架构与学生数据安全边界，不能合入。不得把 Provider key、临时令牌、厂商基础地址或签名逻辑放入 `apps/web` 构建产物。

## 6. 本地环境与验证

### 6.1 前置条件

- Go 1.25 或更高版本；`go.mod` 当前声明 `go 1.25.0`。
- Node.js 22 或更高版本、npm 10 或更高版本；根 `package.json` 有相同 engines 约束。
- 不要使用 Node 20：当前前端测试依赖在 Node 20 下无法正常启动。
- 一个真实 PostgreSQL 实例。集成测试必须通过 `TEST_DATABASE_URL` 指向可用于测试的真实数据库；未配置导致的 skip 不算通过。
- 推荐安装 Docker，用仓库 Compose 启动本地 PostgreSQL；浏览器端到端测试还需要 Chromium。

### 6.2 启动应用

以下步骤只使用本地配置文件。不要把任何密码、密钥或连接串提交到仓库。

```sh
docker compose up -d postgres
cp .env.example .env
# 在 .env 中填写你自己的本地开发占位值，不要使用生产或共享环境凭据。
set -a
source .env
set +a
go run ./server/cmd/migrate
go run ./server/cmd/api
```

另开终端启动前端：

```sh
npm ci
npm run dev
```

首次运行浏览器端到端测试前安装测试浏览器：

```sh
npx playwright install --with-deps chromium
```

未配置真实 Provider 时，可以运行现有自动化测试，但这只证明适配边界和 mock（模拟 Provider HTTP 服务）行为，不证明豆包或千问的真实响应质量、延迟、结构化输出稳定性或计费准确性。

### 6.3 五条门禁

在仓库根目录依次执行：

```sh
go vet ./server/... ./schemas/...
go test -count=1 ./server/internal/... ./server/cmd/... ./schemas/...
CI=1 TEST_DATABASE_URL='<REAL_POSTGRES_TEST_DATABASE_URL>' go test -count=1 ./server/tests/integration
npm test && npm run lint && npm run build
npm run test:e2e
```

`<REAL_POSTGRES_TEST_DATABASE_URL>` 是明显占位符，必须替换为团队自己的隔离测试库连接串，不得把真实值写入文档、提交、日志或聊天记录。

集成测试的当前本地验收基线是顶层 `PASS >= 124`、`FAIL = 0`、`SKIP = 0`，并且 `TestIntegrationDatabaseConfiguredInCI` 必须通过。**顶层计数**只统计测试名中不含 `/` 的测试；带 `/` 的条目是子测试，不重复计入顶层 PASS 数。新增测试可能让 PASS 增加，但不得降低既有阈值或允许 skip。

课堂提交、取消、租约和超时测试对慢机器更敏感。发现偶发失败时，先保留完整错误，再在受限调度下重复目标测试，例如：

```sh
GOMAXPROCS=1 go test -count=100 -run '<TARGET_TEST_NAME>' ./server/tests/integration
```

`-count=N` 应取足够大的重复次数，不能用一次通过证明不存在时序问题。不得通过增大固定 sleep、放宽断言、跳过或删除测试来制造绿色结果。

## 7. 不得改动或绕过的边界

### 学生答案非披露

公开题目与私有答案在数据库和服务端 DTO（数据传输对象）中分离。Student API、学生 WebSocket DTO 和前端状态不得序列化私有答案字段，也不得新增能把答案发给学生或其他角色的接口。Provider 只获得完成其职责所需的数据；私有答案只能进入受控分析/审核路径，不能出现在学生可见输出、普通日志或诊断事件中。

### 掌握状态由规则引擎决定

模型可以返回诊断和建议动作，但不得直接写入 mastery（掌握状态）、证据、复习队列或学习计划。最终状态转换由服务端规则引擎和确定性校验决定。

### 内容发布闸门与四阶段课堂

运行时课堂只能使用 `RELEASED` 内容，不能读取 `DRAFT`。内容必须保留来源、结构校验、独立审核、发布、隔离和审计历史。已经激活的四阶段课堂顺序为 `ORIGINAL -> VARIANT -> ABSTRACT -> VERIFY -> COMPLETE`，各阶段必须真实发生并产生对应证据；Provider 不得跳阶段、伪造完成或改变服务端授权动作。

### 超时关系不变量

不得只为适应某个厂商而单独修改 75 秒服务端提交预算、85 秒代理预算、90 秒会话恢复阈值或前端镜像值。先通过真实延迟数据判断 Provider 是否能在现有总预算内完成生成、审核和有限重试。

### 每次调用都要记录用量与价格版本

每次 AI、STT 和 TTS 请求都必须记录实际 Provider、Model、用途、请求 ID、延迟、相应用量和所用 `ai_price_catalog` 行。价格不存在时请求应在联网前失败；调用成功但用量记录失败时也不能把结果当作正常成功继续发布。

## 8. 发布流程边界

Provider 对接应在独立功能分支完成单元、真实 PostgreSQL 集成、前端和浏览器端到端验证，并经过代码审查。锁定并推送完整 40 位 SHA 后，只有该 SHA 的 CI 全绿，才允许按测试发布流程部署到测试环境。测试环境需要完成服务器核验、浏览器冒烟和产品实际体验；体验结束后先回滚并验证恢复，测试通过才创建以 `main` 为 base 的功能 PR。不得先合并到 `main` 再测试，也不得由实现方自行合并功能 PR。

完整流程见 `docs/deployment/一键发布.md`。该文档包含维护方操作细节；对外交接和代码评审中不得复制发布凭据、服务器地址、令牌、数据库内容或其他基础设施秘密。

## 9. 已知未完成项

以下事项仍未完成，不能在方案、测试报告或发布说明中写成已经具备：

- 尚未接入或验证任何真实模型厂商。当前工程证据来自 Provider HTTP mock 和确定性测试，不等于豆包、千问或其他真实服务验证。
- 四阶段结构化课堂目前只激活 `MATH-LINEAR-EQUATION` 一个知识点，覆盖 142 个已发布知识点中的 1 个；其余 141 个仍走旧课堂路径。
- 误概念分类体系只覆盖 142 个已发布知识点中的 15 个，127 个知识点没有误概念关联；这些知识点只能以空误概念列表通过当前 fail-closed 校验，诊断和复习队列覆盖仍有限。
- 尚未开展能作为产品证据的真实儿童连续使用验证。自动化夹具和人工页面操作不能替代真实儿童验证。
- 当前开发基线 SHA 的 CI 仍失败，详见第 2 节；在同一 SHA 的 CI 全绿前不得发布到测试服务器。

## 10. 对接完成时应交付的证据

对接团队提交评审时，至少应给出：

- 两家 Provider 各自五方法的契约测试，以及网络错误、超时、429/5xx、无效 Schema 和取消传播测试；
- 价格不存在时网络调用次数为零的证据，以及成功调用绑定正确价格版本和用量记录的证据；
- 生成方与审核方身份不同、来源可验证，且任一审核故障都会阻止持久化、实时推送和 TTS 的证据；
- 使用真实厂商沙箱或受控账户得到的结构化输出兼容性、P50/P95/P99 延迟分位数、重试和 75 秒总预算评估；
- 不包含提示词正文、学生答案、个人信息或密钥的最小化诊断日志；
- 第 6.3 节五条门禁的原始结果，以及真实 PostgreSQL 集成测试的顶层 PASS/FAIL/SKIP 统计；
- 明确区分 mock 证据、真实厂商证据、测试环境体验和真实儿童使用证据，不能相互替代。
