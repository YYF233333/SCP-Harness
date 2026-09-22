# SCP Harness v0 封闭规格与自主实施计划

Status: normative product specification + autonomous bootstrap directive
Date: 2026-09-20

Normative amendment: 2026-09-21，O5 议会明确批准第 10.4 节 Claim 保留值及 resource.propose 双 capability 规则。

Corrective rulings: 2026-09-21，O5 明确要求关闭 R1–R4，并为 R4 增加第 25/29 节的封闭 attribute inspection 规则与独立调用预算；修复后提交第二次独立审核。

## 0. Bootstrap implementation contract

本文是 SCP Harness v0 的**封闭产品规格**，同时也是从空仓库开始的一次 autonomous bootstrap directive。

这次 bootstrap 没有人工阶段 Gate。实现者可以连续工作、试错、重构、重写测试、改变实现顺序，直到得到满足本文全部规范的完整系统。不得因为完成某个 Phase、遇到局部测试失败或某个中间实现不可行而停止整个任务；应继续诊断、修改或丢弃失败实现。

本项目的第一目标是：做出小、机械、可测试、失败模式明确的控制系统。不是构建通用 agent platform。

### 0.1 Authority

本文中的 normative requirement 是最终产品唯一 authority。

实现代码、测试、注释、commit history、实现者自行生成的文档和中间设计都不能重新定义、弱化或覆盖本文。测试是规格的验证手段，不是规格本身；“测试通过”不能豁免任何未满足的 normative requirement。

概念设计 `scp_harness_v0_final.md`、role-card 说明 `scp_harness_role_card_v0.md`、机械 schema `scp_harness_role_card_v0.schema.json` 与 frozen acceptance fixture `scp_harness_v0_walkthrough.md` 是本文的冻结上游工件；它们必须与本文一起提供给实现者。若概念层与本文出现真正冲突，以本文对 v0 可执行行为的更具体定义为准；若 role-card JSON Schema / walkthrough fixture 与本文冲突，则视为规格包不一致，FINAL ACCEPTANCE 不得开始，不能由实现者自行选择一个版本。

### 0.2 Closed-world rule

所有设计事项只允许属于两类：

1. **Normative**：本文规定了外部行为、authority、resource、persistence、protocol、isolation、failure 或 architecture semantics，实现必须精确遵守。
2. **Implementation-defined**：本文明确没有冻结的内部实现细节，实现者可以自行选择，只要不改变任何 normative behavior。

不存在第三类“本文没写，但实现者可以猜一个产品语义”的空间。

以下默认属于 implementation-defined：私有 helper 的名字与签名、单个 package 内部文件拆分、SQLite 的物理表拆分细节、索引选择、局部算法数据结构、测试组织、临时 commit 历史。除非本文另有明确要求，这些都不得上升为新的产品抽象。

### 0.3 Autonomous implementation freedom

实现者可以：

- 自主决定实现顺序；
- 交错实现多个 subsystem；
- 添加、删除或重写自己创建的测试；
- 重构尚未完成的代码；
- 丢弃失败实现并重新实现；
- 使用任意数量临时 commit；
- 在本文允许的 package 内自行设计私有函数、类型和 SQLite 物理布局。

这些自由不得改变 observable CLI/config contract、Core authority semantics、resource conservation、worker protocol、Git/WSL isolation boundary、failure model、global serialization 或 architecture constraints。

### 0.4 Ambiguity fallback

如果实现时遇到本文确实未定义、且无法从已有 normative rule 唯一推出的边界情况，不得发明新的外部能力、新 authority、新资源来源、新 capability、新 protocol field、新并发语义、provider abstraction 或远程执行机制。

固定 fallback：

1. fail closed；
2. 不产生新的 authority mutation；
3. 不 mint resource；
4. 不丢失已经持久化的 history；
5. 不扩大 capability；
6. 不执行未明确授权的 external side effect；
7. 返回确定性的显式错误并记录 audit context。

该 fallback 只用于真正未定义的边界，不得覆盖本文已经定义的正常路径。

### 0.5 Milestones are not gates

第 46 节 Phase 0-10 只是推荐 implementation milestones。实现者可以跳序、交错、重做或合并实现步骤。

原 Gate 0-10 全部改称 **Acceptance Case A0-A10**。它们只在最终验收时共同成立，不构成中间停止点，不需要人工批准，也不要求每 Phase 一个 commit。

### 0.6 Single final gate

只有一个 Gate：**FINAL ACCEPTANCE**。

只有当最终 repository 同时满足以下条件时，才允许报告 `SCP Harness v0 implementation complete`：

- 本文全部 normative requirement 已实现；
- Acceptance Case A0-A10 全部通过；
- 第 44 节 mandatory tests 全部通过；
- 第 45 节 architecture constraints 全部通过；
- `go test ./...` 通过；
- `go vet ./...` 通过；
- Windows host integration 通过；
- dedicated `SCP-Worker` WSL2 integration 通过；
- frozen Vorton walkthrough 全自动通过；
- 没有 skipped acceptance test、temporary bypass、mocked production path、未实现的 TODO/FIXME、为了过测试而放宽的 production invariant。

若任一 normative requirement 未满足，系统就是未完成；不得用“基本完成”“核心已完成”替代最终验收。

## 1. 固定产品范围

### 1.1 支持范围

- Windows 11 host。
- WSL2 worker environment。
- 单用户。
- 单 Core 数据库。
- 单 scheduler。
- 全系统同时最多一个 active Attempt。
- 本地 Git repository。
- 任意可执行 worker。
- 一个 Task 绑定一个 authoritative repository。
- 一个 repository 最多一个 ACTIVE Task。
- 一个 mutation/review/rework/promotion chain。
- CLI 操作。
- SQLite 持久化。
- immutable Artifact。
- suspend/resume。
- interrupt。
- crash recovery。

### 1.2 明确不实现

不得实现：Codex adapter、DeepSeek adapter、model/provider abstraction、Agent SDK、worker RPC、MCP integration、Web UI、Docker、Kubernetes、Temporal、LangGraph、Paperclip、OpenHands、PostgreSQL、Redis、message queue、distributed scheduler、remote runner、parallel Attempt、multi-tenant、GitHub/GitLab API、PR API、semantic Git history analysis、arbitrary plugin system、generic storage abstraction、generic repository abstraction、generic sandbox abstraction、generic agent runtime abstraction。

除非以后出现第二个真实实现，不得提前抽象 interface。

## 2. 技术栈

固定使用：

- Language: Go
- Database: SQLite
- Host: Windows 11
- Worker OS: dedicated WSL2 distro
- Repository: Windows `git.exe` for authoritative repository operations
- Artifact: tar blob + SHA-256
- Configuration: JSON
- CLI: Go standard library `flag`/manual subcommand parsing；不得引入 Cobra/urfave/kingpin
- Logging: Go `slog`

SQLite 固定使用 `database/sql` + `modernc.org/sqlite` 纯 Go driver。允许的第三方 Go module **只有 `modernc.org/sqlite` 及其由 Go module graph 自动引入的 transitive dependencies**；生产代码不得直接 import 其他第三方 module。

`modernc.org/sqlite` 的具体版本属于 bootstrap implementation-defined，但一旦写入最终 `go.mod` 就必须固定，不得使用浮动 branch/pseudo update mechanism。不得使用 ORM、query builder、migration framework、DI framework、CLI framework、workflow framework。

Go 标准库优先。任何需要第二个直接第三方 dependency 的实现方案都视为违反 v0 architecture constraint，应重做而不是等待批准。

## 3. Repository 目录结构

最终仓库允许以下 product package / top-level layout；这是 v0 package allowlist，不得自行增加新的 `internal/` package 或架构层：

```text
scp-harness/
    cmd/
        scp/
            main.go
    internal/
        config/
        model/
        store/
        core/
        ledger/
        scheduler/
        worker/
        wsl/
        artifact/
        gitrepo/
        boundedexec/
    migrations/
    testdata/
        workers/
        repos/
        artifacts/
    scripts/
        setup-worker-wsl.ps1
    docs/
        worker-protocol.md
        operations.md
        spec/
            scp_harness_v0_final.md
            scp_harness_v0_execution_plan.md
            scp_harness_role_card_v0.md
            scp_harness_role_card_v0.schema.json
            scp_harness_v0_walkthrough.md
    scp.example.json
    go.mod
    go.sum
```

可以在上述目录内自由增加 `.go`、`_test.go`、SQL migration、test fixture 和文档文件。可以省略暂时为空的目录，但最终实现所需逻辑必须落入上述 package 集合。

不得创建 `domain/application/infrastructure/services/adapters/ports/providers/plugins/framework/repository/sandbox` 等新架构 package。不得为一个实现建立 interface；只有本文已经存在的机械边界（例如 opaque worker executable）可以直接以 concrete struct/function 表达。

## 4. Core 数据模型

### 4.1 Task

至少存储：

- id
- objective
- repo_path
- repo_ref
- responsible_actor_id
- status: ACTIVE / SUSPENDED / CLOSED
- current_authoritative_sha
- state_revision
- created_at
- updated_at

Task 创建必须显式提供资源预算。禁止 Core 自动生成默认预算。

### 4.2 Option

至少存储：

- id
- task_id
- text
- status: OPEN / CLOSED
- resource_parent_id
- created_against_repo_sha
- created_against_state_revision
- created_by_actor
- created_at

Option 文本创建后不可修改。refine、split、merge 都创建新 Option。

增加语义 lineage 表 option_edges(parent_option_id, child_option_id, relation)。relation 仅记录 provenance。不得根据 lineage 推导执行依赖、completion、调度顺序或 requirement。

## 5. Claim

存储：id、task_id、subject_type、subject_id、claim_type、payload_json、issuer_actor_id、created_against_repo_sha、created_against_state_revision、created_at。

worker 输出中不得接受 issuer_actor_id。Claim issuer 永远由 Core 根据 Attempt 的 actor_instance 填写。

## 6. Artifact

存储：id、task_id、semantic_anchor_option_id、source_attempt_id、blob_path、sha256、size_bytes、base_repo_sha、created_at。

Artifact 不包含 status、budget、completed、approved、rejected；这些全部通过 Claim/event 表达。

## 7. Attempt

Attempt 只作为运行记录。字段至少包括：

- id
- task_id
- operation
- target_type
- target_id
- actor_instance
- worker_profile
- resource_anchor_type
- resource_anchor_id
- lease_wall_ms
- created_against_repo_sha
- created_against_state_revision
- started_at
- ended_at
- status: PREPARING / RUNNING / RETURNED / TIMED_OUT / INTERRUPTED / CRASHED / TERMINATED
- exit_code
- termination_reason
- stdout_path
- stderr_path
- produced_artifact_id nullable

Attempt 不允许增加 child attempts、dependency graph、planning status、semantic completion、workflow edges。

`TERMINATED` 用于“Attempt 因控制面/基础设施条件而终止，且不应解释为 worker 的语义失败”的情况。具体原因只写入 `termination_reason`；Core 不从错误文本推断原因。

## 7A. SQLite persistence contract

SQLite 是 Core authority state 的唯一数据库。数据库物理 DDL 属于 implementation-defined，但以下语义是 normative：

- migration 必须 deterministic、可重复检测当前版本，失败时 fail closed；
- 开启并实际检查 foreign-key enforcement，或使用等价的显式事务约束保证不存在 dangling semantic references；
- 一个 repository identity 在任意时刻最多存在一个 ACTIVE Task binding；
- immutable Option / Artifact 的 immutable 字段一旦 insert 后不得 update；
- Task create/extend、Option split/merge/allocate/close、Attempt lease reserve/settle、worker semantic result apply、blocker create/resolve 都必须各自在单个 SQLite transaction 中保持原子性；
- 事务失败不得留下部分 authority mutation；
- Git CAS 与 SQLite 不可能形成同一 ACID transaction，唯一允许的跨系统协议是第 28 节 promotion journal；不得自行设计第二套 distributed transaction/reconciliation framework；
- `PRAGMA`、journal mode、索引布局、表拆分方式属于 implementation-defined，只要 crash/recovery 和所有 invariant 满足本文。

数据库不是公共 API。最终验收检查行为与 invariant，不要求某个固定表名集合；本文明确点名的 `repo_update_journal`、`runtime_blockers` 除外，因为 recovery/failure semantics 直接依赖它们。

## 8. ResourceLedger

v0 只实现一个强制资源维度：`wall_ms`。不得在 v0 实现 CPU/token/tool-call 等第二个 ledger dimension。Role-card 中即使存在其他 limit/influence key，v0 ledger 也不为其建立资源账户。

资源树固定为：

```text
Task root account
  -> Option account
       -> child Option account ...

Attempt lease 不是长期账户，只是从一个 resource anchor 临时 reserve 的 outstanding lease。
```

每个 Option 恰好一个 resource parent account。Artifact 永不拥有 budget。

### 8.1 唯一 mint 边界

只有两种操作可以增加一个 Task 内的 `total_minted_wall_ms`：

1. `task create` 的显式 initial budget；
2. `task extend` 的显式 positive extension。

两者都必须由具有对应 capability 的 actor 发起。不存在默认 budget、隐式 top-up、自动续费、失败补偿 mint、merge mint 或 worker mint。

`task extend` 只允许 ACTIVE 或 SUSPENDED Task；CLOSED Task 永远不能重新 mint 或 reopen。

### 8.2 Conservation invariant

对每个 Task，任何稳定持久化状态都必须满足：

```text
total_minted_wall_ms
=
Σ remaining_wall_ms(all Task resource accounts)
+ Σ outstanding_attempt_lease_wall_ms
+ total_charged_wall_ms
+ retired_wall_ms
```

其中 `retired_wall_ms` 表示**曾被合法 mint、最终未消费、且因 Task 永久关闭而撤销的剩余授权**。它是 ResourceLedger 的累计量，不是新 semantic object，也不能再次转回可用余额。

实现可以存储或从 ledger/audit records 推导 `total_minted_wall_ms`、`total_charged_wall_ms` 与 `retired_wall_ms`，但必须能够在 property test 中机械验证上述等式。

始终要求：

```text
remaining_wall_ms >= 0
outstanding_lease_wall_ms >= 0
total_charged_wall_ms >= 0
retired_wall_ms >= 0
```

除 create/extend 外，所有 allocate/refine/split/merge/close/reserve/refund/settle/retire 都必须保持 `total_minted_wall_ms` 不变。Option close 只把余额返回 parent，不增加 retired；**只有 Task close 可以增加 `retired_wall_ms`**。

### 8.3 Transfer

所有真实转账必须在 SQLite transaction 内完成。金额必须是 integer `wall_ms >= 0`。

一般 move：

```text
source.remaining -= N
destination.remaining += N
```

前置条件：两个账户属于同一 Task，`source.remaining >= N`，且该 operation 的 resource-lineage rule 允许这条 move。任一检查失败则整个 transaction rollback。

不得通过创建新 account 时复制 source balance；新 account 初始余额默认为 0，随后由同一 transaction 内的真实 move 注资。

### 8.4 Refine / split / merge resource semantics

`refine(parent -> child)`：child resource parent = parent Option account；`transfer_wall_ms` 可省略，默认为 0；显式值范围 `0..parent.remaining`。旧 Option 不关闭，零转账不改变 parent balance，child balance=0。

`split(parent -> children[])`：每个 child resource parent = parent Option account；每个 child 显式指定 allocation。原子事务必须满足：

```text
parent_before
= parent_after + Σ child_initial
```

split 不自动关闭 parent。

`merge(participants -> merged)`：所有 participant 必须属于同一 Task。`LCA` 只在 resource-parent tree 上定义，与 semantic lineage 无关：从 participant resource accounts 向 root 走，最深的共同 ancestor account 即 LCA。merged Option 的 resource parent = LCA。

merge request 必须为每个 participant 显式给出 `transfer_wall_ms`；Core 不自行“拿剩余全部余额”。每个 source 扣除指定金额，merged account 增加总和，事务后：

```text
Σ participant_before + merged_before
= Σ participant_after + merged_after
```

若任一 source 余额不足、participant 跨 Task、LCA 不存在或输入重复，整个 merge 失败且不创建 merged Option。

`close(option)`：Option unused remaining 全额返回其 immediate resource parent，然后 Option -> CLOSED。关闭不删除账户/history。

### 8.5 Attempt lease

每个 Attempt 创建时必须解析**唯一一个** resource anchor，然后显式 reserve：

```text
0 < lease_wall_ms <= anchor.remaining_wall_ms
anchor.remaining -= lease_wall_ms
outstanding_lease = lease_wall_ms
```

可用 lease 固定为 `min(anchor.remaining_wall_ms, worker_profile.timeout_ms, actor_card.limits["lease.wall_ms"])`。`lease.wall_ms` 是 v0 唯一具有 Core mechanical effect 的 role-card limit，必须为 positive integer；不存在该字段的 role card schema-invalid。若结果 <= 0，Attempt 不得启动。

Attempt 正常结束：

```text
actual = min(measured_elapsed_wall_ms, lease_wall_ms)
charged += actual
refund = lease_wall_ms - actual
anchor.remaining += refund
outstanding_lease = 0
```

timeout：charge full outstanding lease。

Core crash 或无法确定精确计量：charge full outstanding lease。

`WORKER_UNAVAILABLE` 且 worker executable 尚未真正开始：只 charge 已可靠测得的准备/执行 active wall time，剩余 refund；若计量不确定仍按 full outstanding lease 处理。

不得为了更精确退款引入复杂恢复协议；保守收费优先。

### 8.6 Resource anchor resolution

固定规则：

- discussion：Task root（即使 target=OPTION 且 Option allocation=0）；
- Option-target mutation：该 Option；
- Artifact-target work：Artifact 的 `semantic_anchor_option_id`；
- Task-level/new-route exploration：Task root；
- cross-Option compare / MergeJudge / MergeSynth：participant resource accounts 的 resource-tree LCA；
- protected test：被测 Artifact 的 semantic-anchor Option；
- review / rework：被 review/rework Artifact 的 semantic-anchor Option。

Attempt target 和 resource anchor 在 Attempt 创建后冻结，不因 worker 输出或 context 中发现新 Option 而改变。

## 9. 全局串行执行

v0 使用**一个全局 execution slot**。这比单纯 `global_active_attempt_count <= 1` 更强：任何时刻最多有一个会消费执行资源或改变候选/authoritative state 的 activity。

以下 activity 都占用同一个 global execution slot：

- 任意 worker Attempt（包括 read-only cognitive/review/merge worker）；
- writable workspace materialization/final capture；
- protected test invocation；
- promotion Git construction + CAS；
- `scp recover` 中会修改 runtime/authority state 的 recovery sequence。

因此 mutation -> Artifact capture -> protected test -> review -> reject/rework 或 approve -> promotion 的一条 in-flight chain 在释放 slot 前，不允许另一个 Task/Option 的执行 activity 插入其中。

允许并发的只有不会获取 execution slot 的纯控制/只读操作，例如 `status/list/show` 和读取已完成 Artifact metadata。

`attempt interrupt` 是唯一 out-of-band control exception：它必须能在 slot 被当前 Attempt 占用时发出终止请求。interrupt 本身不能启动另一项 execution activity；原 owner 必须完成 termination/capture/settlement 并释放 slot 后，scheduler 才能启动下一项工作。

Scheduler 不得创建 worker pool、parallel worker goroutine、async task graph 或预实现 future concurrency。内部为了 process I/O/timeout/cancellation 使用必要 goroutine 可以存在，但不得造成两个 execution activity 同时运行。

Scheduler 按 Task 的 created_at,id 检查既有 Pending，再执行一次性 initial exploration，否则 idle。Allocation 不授予执行 authority，也不影响 priority。

## 10. Role card 与 capability registry

Role card 使用严格 JSON，机械 schema 以 `scp_harness_role_card_v0.schema.json` 为准。顶层字段**恰好**为 `schema_version`、`id`、`context`、`capabilities`、`limits`、`influence`；unknown field、duplicate key、missing required field 均 invalid。`schema_version` 必须 exactly `0`。

Core 不识别 O5、MTF、RAISA、researcher、reviewer 等名字；`id` 只有 provenance 意义。Host CLI 使用 `scp.json.operator_actor_card` 指向的 role card 作为 operator identity。Host 用户是可信的“谁在敲命令”，但 Core 仍按该 card 的 capability 做机械授权；不得因为命令来自本机 CLI 而绕过 capability。

### 10.1 Context registry

`context` 是只读 address-space allowlist。v0 只允许以下 channel：

```text
task.objective
task.state
option.target
option.lineage.direct
claim.related
artifact.metadata
artifact.content
test.result
review.findings
ledger.resource
repository.snapshot
```

Core 构造 `context/` 时，只能 materialize role card 明确列出的 channel；未列出的 channel 必须完全缺席。`option.lineage.direct` 只包含 direct parent/child 及最小 metadata，不递归展开。`artifact.content` 只在当前 operation 本来就有相应 Artifact target 时提供。`repository.snapshot` 只有在当前 operation 有 authoritative snapshot **且 actor 同时持有 `repository.read`** 时才可 materialize。context 可见性本身不授予写权限或外部 effect。

`context` 不列出未知/自定义 channel；schema 直接拒绝，以避免拼写错误静默降级。

### 10.2 Capability registry

v0 capability 名称是 closed enum；role card 出现未知 capability 直接 schema-invalid，不得“保留但忽略”：

```text
task.create
task.extend
task.suspend
task.resume
task.complete
option.propose
option.refine
option.split
option.merge
option.allocate
option.release
option.discuss
option.complete
claim.publish
resource.propose
review.decide
attempt.interrupt
artifact.export
repository.read
sandbox.write
process.execute
```

固定 scope 规则：

- `repository.read` 必须且只能 `scope = "task.repository"`；
- `sandbox.write`、`process.execute` 必须且只能 `scope = "lease.sandbox"`；
- 其余 capability 不得带 `scope`；
- 同一 capability name 在一张 card 中最多出现一次。

固定授权矩阵：

- Task create -> `task.create`
- Task extend -> `task.extend`
- Task suspend -> `task.suspend`
- Task resume -> `task.resume`
- Task close / qualifying fulfilled decision -> `task.complete`
- Option proposal/new option -> `option.propose`
- Option refine -> `option.refine`
- Option split -> `option.split`
- Option merge / MergeSynth creation -> `option.merge`
- Option host release -> `option.release`
- Option discussion request -> `option.discuss` + `claim.publish`
- Option resource allocation -> `option.allocate`
- Option close / qualifying fulfilled decision -> `option.complete`
- arbitrary informational Claim -> `claim.publish`
- resource request/proposal Claim -> `resource.propose`；它不直接移动或 mint budget
- reviewer top-level APPROVE/REJECT -> `review.decide`
- interrupt -> `attempt.interrupt`
- artifact export to caller-selected host path -> `artifact.export`
- actor receives authoritative repository snapshot -> `repository.read`
- actor receives writable workspace -> `sandbox.write`
- actor launches worker-side processes -> `process.execute`

Capabilities default-deny。一个 operation 需要多个 capability 时必须全部满足。

### 10.3 Limits and influence

`limits` 在 v0 只有一个允许字段：

```json
{"lease.wall_ms": 600000}
```

它必须为 positive integer，按第 8.5 节参与 Attempt lease 上限计算。v0 不接受 `lease.cpu_s`、token/tool-call 等其他 limit key；这些属于 post-v0。

`influence` 保留为 schema 字段，value 必须是非负 JSON number，但 **v0 Core 不消费任何 influence coefficient**：它不得影响 capability、resource allocation、scheduler priority、review verdict 或 completion。该字段仅为上游概念模型的前向兼容数据；在 v0 中修改 influence 不得改变 Core behavior。

`claim_type = "fulfilled"` 永远可以作为普通 informational Claim 被有 `claim.publish` 的 actor 发布；只有 issuer 同时具有目标类型对应的 `option.complete` 或 `task.complete` 时，它才是 qualifying completion authority。Core 不根据 claim 文本、role id 或模型名字猜测 authority。

Worker 无法自行选择 actor identity；Attempt 的 `actor_instance` 和 role card 由 Core 根据 worker profile 固定。worker result 中出现 issuer/actor/capabilities 字段一律 schema-invalid。

### 10.4 Claim 保留值与资源提议（O5 议会确定化补充）

`claim_type` 继续保持自由文本字段。Core 只对本规范明确列出的保留值赋予特殊治理语义；不得自行增加保留值或启发式识别规则。

只有精确匹配 `claim_type = "resource.propose"` 才是 resource proposal。不得根据前缀、大小写变体、payload 内容、自然语言描述或其他启发式规则推断资源提议语义。

创建 `resource.propose` Claim 时，issuer 必须同时具备 `claim.publish` 与 `resource.propose` 两个 capability。任意一个缺失时，整个 Claim request 必须以 `CAPABILITY_DENIED` 失败，不创建该 Claim，且不得降级为普通 informational Claim。worker result 内其他独立 child request 仍按第 14.6 节处理。

`resource.propose` Claim 仅表示资源请求/提议。本身不得 mint、extend、allocate、transfer、reclaim 或以任何其他方式移动预算；其创建前后 ResourceLedger 必须完全不变。实际资源状态变化仍只能通过本规范已有的显式资源操作及对应 capability 完成。

已有的其他保留语义继续有效：精确的 `claim_type = "fulfilled"` 通常为 informational Claim；issuer 同时具有目标类型对应的 `option.complete` 或 `task.complete` 时，按第 32/33/35 节触发既有 qualifying completion/close 语义。

除本规范明确赋予特殊治理语义的保留 `claim_type` 外，其余任意自由文本 `claim_type` 一律仅作为 informational Claim，不得产生资源、completion、review、promotion 或其他隐式状态转换。例如 `Resource.Propose`、`resource.propose.foo` 以及 type 不匹配但 payload 中写有资源请求的 Claim，均不具有资源提议语义。

本补充仅确定现有 capability/Claim 语义，不新增资源机制。

## 11. Worker 模型

Worker 是完全 opaque 的 executable。Core 不得识别 Codex、GPT、DeepSeek、thread、turn、reasoning、tool、agent、subagent、token、provider。

Worker profile 示例：

```json
{
  "id": "operator",
  "command": ["/opt/scp-workers/operator"],
  "actor_card": "operator",
  "timeout_ms": 1800000,
  "workspace": "writable",
  "synthetic_git": true
}
```

实际数值由配置提供，不得硬编码生产 timeout。

## 12. Worker 文件协议

每次 Attempt 创建：

```text
/scp/attempt/
    input.json
    context/
    workspace/
    result.json
```

提供环境变量 SCP_INPUT、SCP_CONTEXT、SCP_WORKSPACE、SCP_RESULT。

Worker 只需要读取 input/context/workspace，执行任意操作，可选写 result.json，然后退出。Core 不与 worker 进行运行期 RPC。

## 13. input.json

`input.json` 由 Core 生成，UTF-8 JSON，`schema_version` 固定为 `0`。worker 只读。顶层 schema 固定如下；不得新增 provider/model/thread/token 字段：

```json
{
  "schema_version": 0,
  "attempt_id": "128-bit-hex",
  "operation": "mutation|review|option_generation|merge_judge|merge_synth|discussion",
  "objective": "string",
  "target": {"type": "TASK|OPTION|ARTIFACT", "id": "string"},
  "semantic_anchor": {"option_id": "string"},
  "actor_card": {},
  "created_against": {"repo_sha": "40-hex", "state_revision": 0},
  "context": {"root": "/scp/attempt/context"},
  "workspace": {
    "mode": "none|readonly|writable",
    "root": "/scp/attempt/workspace",
    "base_repo_sha": "40-hex-or-empty",
    "source_artifact_id": "string-or-empty",
    "synthetic_git": false
  },
  "output_schema": "mutation|review|option_generation|merge_judge|merge_synth|discussion"
}
```

`semantic_anchor` 对 Task-level option generation 可以是 `null`；对 Option/Artifact-target work 必须非 null。`actor_card` 是冻结后的完整 mechanical role card，不含 provider/model metadata。

`context.root` 只描述允许读取的 context directory，不得把整个数据库 dump 给 worker。`workspace.mode=none` 时目录可以不存在；readonly/writable 时必须存在。

Core 写出的 input 必须满足自己的 schema；若无法构造合法 input，Attempt 不得启动并按基础设施/一致性规则处理。

## 14. result.json

所有 result 都是 UTF-8 JSON，`schema_version=0`，并且使用**严格 schema**：

- unknown field -> invalid；
- duplicate JSON object key -> invalid；
- required field missing/null -> invalid；
- enum 大小写必须精确匹配；
- 所有用户文本 trim 后必须非空；
- result size 必须受配置 cap 约束；
- worker 不得提供 ID、issuer、created_at、created_against、resource parent、allocation 或 capability；这些全部由 Core 生成/验证。

通用 Claim request schema：

```json
{
  "subject_type": "TASK|OPTION|ARTIFACT",
  "subject_id": "existing-id",
  "claim_type": "non-empty-string",
  "payload": {}
}
```

Claim subject 必须存在且属于当前 Task 可见范围；否则该 Claim request 无效。worker claim 只有在 actor 拥有 `claim.publish` 时才创建。Core 固定填充 issuer 与 `created_against`。

### 14.1 mutation

```json
{
  "schema_version": 0,
  "operation": "mutation",
  "disposition": "PROMOTE_FINAL|CONTINUE_FINAL|DROP_FINAL",
  "claims": [],
  "new_options": [{"text": "non-empty"}]
}
```

`claims`、`new_options` 必须存在，可为空数组。`new_options` 只有 actor 有 `option.propose` 时才生效；Core 创建 ID/provenance。若当前 Attempt 有 semantic-anchor Option，新 Option 的 semantic lineage parent 与 resource parent 都是该 anchor；初始 allocation=0。Task-level mutation 无 anchor 时 resource parent=Task root。

result 缺失或整个 schema invalid：固定按 `DROP_FINAL` 处理，不创建 claims/options；writable workspace 仍照常 capture Artifact。

### 14.2 review

```json
{
  "schema_version": 0,
  "operation": "review",
  "verdict": "APPROVE|REJECT",
  "findings": ["non-empty text"],
  "claims": []
}
```

review Attempt 必须由具有 `review.decide` 的 actor 执行，否则整个 result invalid。findings 由 Core 记录为 reviewer provenance 的 finding Claims/events；额外 `claims` 仍要求 `claim.publish`。

result 缺失/invalid/unauthorized：固定产生**conservative synthetic REJECT operation result**，不产生 reviewer approval Claim，不 promotion。该 synthetic reject 只阻止扩大 effect，不得被解释为 Artifact 语义为假。

### 14.3 option_generation

```json
{
  "schema_version": 0,
  "operation": "option_generation",
  "options": [{"text": "non-empty"}]
}
```

actor 必须有 `option.propose`。Core 生成所有 ID/provenance/resource parent，初始 allocation=0。Task-level generation 的 resource parent=Task root；有 semantic anchor 时=anchor Option account。

invalid result -> 创建 0 个 Option，不改变已有 Option。

### 14.4 merge_judge

```json
{
  "schema_version": 0,
  "operation": "merge_judge",
  "groups": [["option-id", "option-id"], ["option-id"]]
}
```

每个 group 必须非空。Core 验证给定 input Option ID 集合中的每个 ID **恰好出现一次**；unknown、duplicate、missing、空 group、跨 Task ID 任一出现则整个 partition invalid。invalid partition 不关闭、不改写任何 raw Option，也不进入 MergeSynth。

### 14.5 merge_synth

```json
{
  "schema_version": 0,
  "operation": "merge_synth",
  "text": "non-empty canonical working text"
}
```

只对一个已经机械验证的 group 调用。actor 必须有 `option.merge`。Core 创建一个新 merged Option，semantic lineage parents=该 group 全部 raw Options，resource parent=participant resource-tree LCA，初始 allocation=0。Raw Options 永久保留且不会因 dedup 自动 close。

**MergeSynth 是 semantic dedup projection，不是第 8.4/33 节的显式 resource merge。** MergeSynth 不从 participant Option 转入任何余额；新 merged Option 永远以 `remaining_wall_ms = 0` 开始。只有 operator/authorized actor 明确执行 `option merge --spec` 时，才应用 participant `transfer_wall_ms`。MergeSynth Attempt 自身仍按第 8.6 节向 participants 的 resource-tree LCA 收费。

invalid result -> 不创建 merged Option。

### 14.6 Partial child-operation failure

顶层 result schema 合法时，内部每个 Claim/new Option request 仍单独做 capability、subject 和 invariant 验证。无权限/无效的 child request 被拒绝并 audit，不得因此获得 authority；其他彼此独立且合法的 child request 可以提交。任何会破坏 ledger/state invariant 的组合必须整笔 transaction rollback。

### 14.7 discussion

```json
{"schema_version":0,"operation":"discussion","text":"non-empty response"}
```

Strict typed schema: unknown/duplicate/missing/null keys, blank text, wrong type,
version or operation are SCHEMA_INVALID. No claims/new_options/release/allocation/
patch/verdict fields. RETURNED + valid + claim.publish creates exactly one
informational discussion.reply on the target Option, with Core-bound Attempt issuer
and created_against. Invalid/crash/timeout/interrupt creates no reply and ends the
request without retry; the human comment remains. No Artifact, Option changes,
resource transfers or mutation follows. Infrastructure blockers preserve the exact
pending step for explicit repair/resolve, as for other operations.

## 15. WSL Runner

使用单独的 WSL2 distro：SCP-Worker。不要在用户已有开发 distro 中运行 worker。

worker distro：不得自动挂载 Windows drives；不得启用 Windows interop；不得包含 authoritative repository；不得包含 SQLite database；不得包含 Artifact store。

/etc/wsl.conf：

```ini
[automount]
enabled=false
mountFsTab=false

[interop]
enabled=false
appendWindowsPath=false
```

Worker distro 内只保留 worker executable、worker 自己需要的 runtime、worker 自己需要的 credentials、temporary Attempt workspace。

v0 trust assumption：configured worker executable is trusted software；model behavior is not trusted。不承诺抵御主动 malicious native executable。

## 16. Worker 生命周期

固定执行：

1. ensure no active Attempt
2. prepare DB Attempt record
3. create/clean /scp/attempt
4. materialize input.json、context/、workspace/
5. optionally create synthetic Git
6. mark Attempt RUNNING
7. launch worker
8. wait for normal exit / timeout / interrupt
9. terminate worker environment
10. capture stdout/stderr
11. capture Artifact if writable workspace
12. parse result.json
13. validate each requested semantic operation
14. apply valid operations in DB transactions
15. settle lease
16. mark Attempt terminal

Worker 退出不等于 Option complete、Task complete、Artifact approved。

## 17. Interrupt

scp attempt interrupt 必须：

1. mark interrupt requested
2. revoke current Attempt lease logically
3. wsl --terminate SCP-Worker
4. restart distro only for workspace capture
5. capture writable workspace Artifact
6. mark Attempt INTERRUPTED
7. charge consumed/full uncertain resource
8. release mutation chain

interrupted Option 保持 OPEN。默认 disposition = DROP_FINAL。不得自动从 interrupted Artifact continuation。

## 18. Artifact 实现

Git 不负责 Artifact。

Artifact 格式：artifact-id/workspace.tar + meta.json。

meta 至少含 sha256、size_bytes、source_attempt、semantic_anchor_option、base_repo_sha。

捕获过程：worker 已停止 -> 扫描 workspace -> 检查文件数量/总大小 -> 拒绝不支持的特殊文件 -> 生成 tar 到临时文件 -> SHA-256 -> fsync -> atomic rename -> SQLite insert。

v0 Artifact 支持 regular file 和 directory。暂不支持 device、fifo、socket、hard link、symlink；遇到时 Attempt 不得 promotion，并记录明确错误。

## 19. Artifact continuation

CONTINUE_FINAL：Artifact Pn -> 下一次 Attempt -> 解包到新的 workspace。不得复用旧 workspace。每次 Attempt 都创建新的工作目录。

## 20. Reviewer workspace

Reviewer 输入 Artifact 时解包 disposable copy，并将文件权限设为只读。Reviewer 的任何写入失败。即使 reviewer 成功创建其他临时文件，也不得进入 Artifact store。review 永远不生成 workspace Artifact。

## 21. Protected Test Runner

Test Runner 不是 worker plugin。`scp.json.protected_test` 指定固定 command、timeout_ms、output_limit_bytes。候选 workspace 不得修改这些值。

Protected Test Runner 固定在 dedicated `SCP-Worker` WSL2 distro 中执行，但**不是 worker profile/Attempt**：Core 将 Artifact 解包到 fresh disposable **writable** test copy（允许编译器/测试写 build/temp 输出），从 copy root 启动固定 command，capture stdout/stderr/exit/timeout，随后无条件丢弃整个 test copy。test copy 的任何变化不得进入 Artifact store 或 Promotion。

Protected test 占用 global execution slot，并直接从 Artifact semantic-anchor Option 的 wall budget reserve/charge；可用 test lease = min(Option remaining, configured test timeout)。不得相信候选文件中的 runner config、resource config、timeout config。timeout/runner failure 不得被解释为 test PASS。

## 22. Git 的唯一职责

Git 只负责：

1. resolve authoritative ref
2. export exact authoritative tree
3. construct promoted commit
4. CAS update authoritative ref

Git 不负责 SCP history、Artifact history、Option history、Claim history、Attempt history、context search、dedup、scheduler、provenance database。

## 23. Git 强制封装

Core 发起的所有 Git command construction 只能存在于 `internal/gitrepo`，并且所有实际 subprocess 都必须经 `internal/boundedexec`。

这里区分两类 Git：

1. **Core Git invocation**：authoritative resolve/export/promotion，以及 Core 为 synthetic workspace 执行的 `git init/add/commit`。受第 23-29 节全部限制和 call-count test 约束。
2. **worker sandbox Git**：worker 启动后，在隔离的 synthetic repo 内自行执行的任意 git command。它属于 opaque worker sandbox command，不是 Core Git invocation，不计入 Core Git call count，也不受第 24 节 forbidden command list 限制；安全边界来自它看不到 authoritative `.git`/history。

因此 worker 可以在 synthetic one-commit repo 中运行 `git log`/`git rev-list` 等普通开发命令；Core 自己绝不能用这些命令做业务/history 查询。

Architecture test 必须扫描生产 Go 源码：除 `internal/gitrepo` 外不得出现构造 `git.exe`、`git` Core subprocess 的代码；除 `internal/boundedexec` 外不得直接使用 `os/exec` 启动外部进程。

## 24. Git 禁止命令

生产代码不得运行：git log、git rev-list、git blame、git bisect、git reflog、git merge-base、git branch --contains、git log --all、git fsck、git gc、git repack、git fetch、git pull。

不得通过 Git history 获取 context。不得遍历历史完成业务逻辑。

## 25. Git 允许操作

Resolve：`git -C <repo> rev-parse --verify <ref>^{commit}`，必须有 timeout/stdout cap/stderr cap。

Export：`git -C <repo> archive --format=tar <exact-sha>`，流式读取，限制 maximum bytes 和 maximum duration。不得 clone，不得 checkout historical worktree。

### 25.1 R4 archive attributes — O5 FINAL RULING

本节取代所有此前 R4 Set / Unset / Unspecified / string-value 求值规则、typed ls-tree / check-attr 方案及其 inspection 预算。R4 的目标只有：exported snapshot 保持 authoritative exact SHA 的树内容，不能因 archive attributes 删除路径或替换内容。

`export-ignore` 与 `export-subst` 是 unsupported repository feature。Exact SHA 内所有 tracked、basename 为 `.gitattributes` 的 blob 均在检查范围；其中出现任一保留标识符即以 `REPOSITORY_UNAVAILABLE` 拒绝。无需判断 comment、pattern 匹配、覆盖规则、否定、unspecified、显式 value 或 macro；这些都允许保守拒绝。不得增加 attribute parser、四态恢复逻辑、目录属性模拟器或兼容框架。

检查必须绑定 exact SHA；不得用 mutable worktree/index 替代。R4 最多一个 `export_attr_inspection` Git subprocess，可用限定 exact tree 和 `.gitattributes` pathspec 的 `git grep`。Match 拒绝；明确的 clean no-match 才继续。Git failure、输出异常、超出边界、无法确定结果都必须 `REPOSITORY_UNAVAILABLE`，不运行 archive。

Archive 的非 tree attribute 来源必须被隔离：不得使用 `--worktree-attributes`；`$GIT_DIR/info/attributes` 必须不存在、为空或由受控执行环境保证无法提供 archive attributes；global/system attributes 必须显式隔离/禁用。Core 不得依赖调用用户的 Git 配置来决定导出内容。无法建立隔离则 `REPOSITORY_UNAVAILABLE`。普通、不含两个标识符的 `.gitattributes` 以及没有 `.gitattributes` 的仓库不因 R4 被拒绝，原有 exact-content 与 no-op round-trip 保证继续有效。

属性检查与 archive 均为 Core Git invocation，只能在 `internal/gitrepo` 构造并经 `internal/boundedexec` 执行；timeout、输入、输出均有界。不得 clone、checkout historical worktree、history traversal、增加额外 inspection subprocess 或新 repository abstraction。

## 26. Worker synthetic Git

默认 worker 看不到 authoritative `.git`。workspace 必须来自 `git archive <exact SHA>` 或 continuation Artifact，不得复制 authoritative `.git`。

如果 profile `synthetic_git=true`，Core 在 worker 启动前于 WSL workspace 内机械执行：

1. `git init`
2. `git add -A`
3. `git -c user.name=SCP -c user.email=scp@local commit -m "SCP base"`

这些是 **Core Git invocations**：命令由 `internal/gitrepo` 构造，经 `internal/boundedexec` 启动 WSL Git，并计入 `synthetic_git <= 4`。不得用额外 `git config` subprocess 消耗调用次数。

初始化完成后该 repo 必须：exactly one commit、无 remote、无 authoritative refs/history。随后 worker 在其中自行使用 Git 属于 sandbox behavior，不计入 Core Git invocation。

synthetic `.git` 永不进入 Artifact：Artifact capture 必须排除 workspace 根的 `.git` directory。Promotion 永远根据 Artifact files + authoritative base 构造新 commit，绝不采用 worker commit/object database。

## 27. Promotion

Promotion 必须从 Artifact 构造 authoritative commit。禁止 worker 自己的 commit 被直接采用。

流程：

1. base = Artifact.base_repo_sha
2. assert target ref == base
3. extract Artifact into bounded Windows temp tree
4. create temporary Git index
5. git read-tree base
6. GIT_INDEX_FILE=<temp>, GIT_WORK_TREE=<artifact-tree>, git add -A
7. git write-tree
8. git commit-tree <tree> -p <base>
9. git update-ref <target> <new> <base>

最后一步必须是 CAS。

如果 target 已经不是 base：promotion abort，记录该 promotion operation 为 `PRECONDITION_CHANGED`；保留 Artifact，Option/Task semantic status 不因此改变，不创建 infrastructure blocker。不得 merge、rebase、force push、automatic conflict resolution。

## 28. Promotion journal

SQLite 增加内部运行表 repo_update_journal：id、task_id、artifact_id、target_ref、old_sha、new_sha、state(PREPARED/APPLIED/CONFLICT)、created_at。

它是内部 recovery journal，不是新的 semantic object。

流程：DB PREPARED -> git update-ref CAS -> DB APPLIED。

Core crash 后：target == new_sha -> mark APPLIED；target == old_sha -> retry CAS；otherwise -> mark CONFLICT。

## 29. Git 调用必须有界

所有 **Core Git invocation** 通过 `internal/boundedexec` 执行。boundedexec API 的具体 Go 函数签名属于 implementation-defined，但每次调用方必须显式提供/解析出有限的 timeout、max stdout、max stderr；禁止任何 unbounded `exec.Command(...).Run/Output/CombinedOutput` 路径。

Git 调用次数 acceptance：

```text
resolve_ref <= 1
export_attr_inspection <= 1
export_tree <= 1
synthetic_git <= 4
promotion <= 8
```

计数单位是 Core 启动的 Git subprocess。worker 启动后自己在 sandbox 中执行的 Git 不计数。

若某个候选实现超过这些上限，该实现不符合规格；实现者必须重做，不能自行放宽上限。

## 30. Context

v0 不实现 RAG。每次 Attempt 构造 context/，只放该 role card 有权读取的信息。内容使用普通 JSON、text、directories。worker 自己使用 find/grep/cat。Core 不预测 worker 应该读什么。不得自动把全部 context 放进 prompt。

## 31. created_against

Task 保存 state_revision。每次 authoritative Core state mutation：state_revision += 1。

Attempt 启动时冻结 repo_sha + state_revision。所有该 Attempt 产生的 Option、Claim、summary/projection 继承此 created_against。不得因为 repository 更新自动重写旧记录。

## 32. Task lifecycle

Create：检查 operator actor 有 `task.create`、objective/repo/ref/responsible actor/initial wall budget 全部显式提供、budget > 0、repo currently unbound、Git ref resolvable；创建 Task + root resource account，mint exactly initial budget，bind repo，Task -> ACTIVE。

Extend：检查 actor 有 `task.extend`、Task 为 ACTIVE 或 SUSPENDED、amount > 0；exactly mint amount 到 Task root。CLOSED Task 不可 extend/reopen。

Suspend：检查 actor 有 `task.suspend`；若当前 Task 占有 execution slot，则请求 interrupt/cancel 并等待 chain 机械终止；preserve Artifact/history；freeze resource accounts；release repository binding；Task -> SUSPENDED。Suspend 不改写旧 Option/Claim/Artifact 的 `created_against`。

Resume：检查 actor 有 `task.resume`、Task=SUSPENDED、repository free；resolve 当前 repo SHA；rebind repo；Task -> ACTIVE；更新 Task 的 current authoritative SHA，但不得修改历史 `created_against`。

Close：检查 actor 有 `task.complete`。该操作等价于由该 actor 创建一个 subject=Task、claim_type=`fulfilled` 的 qualifying Claim，然后：若有 active chain 先按 interrupt/cancel 规则机械终止并 settle 所有 outstanding lease；disable descendant Options；在一个 SQLite transaction 中计算该 Task **所有 resource account 的 remaining 总和 R**，把这些 account 的 remaining 全部置 0，并执行 `retired_wall_ms += R`；release repo binding；Task -> CLOSED。CLOSED terminal，不可 resume。Task close 后第 8.2 节 conservation equation 仍必须精确成立，retired resource 永远不可恢复或再次分配。

### 32.1 Task 控制操作互斥（O5 R1b 裁决，2026-09-21）

Core 必须按同一数据库身份和 Task ID 实施跨 CLI 进程生效的控制互斥锁。`task suspend/resume/close`、`option close`、具有对应 completion capability 的 `fulfilled` Claim，以及其他能够发起或解除该 Task 取消保护的外部入口，均使用所属 Task 的同一把锁。锁覆盖完整操作：取得锁后检查状态、请求取消、等待已有执行终止和结算、提交最终状态，最后释放锁。竞争请求直接返回 `BLOCKED`，不得隐式重试；内部共用步骤沿用调用方已经取得的锁，不重复获取。

已有执行的终止、capture、settlement、释放 execution slot、取消监视和状态读取必须能在持锁期间继续进行，不得等待同一把控制锁。SQLite 只承担短事务，等待进程结束时不得持有 SQLite 写事务。控制锁只负责控制请求互斥；既有 execution slot 继续负责执行活动互斥。调度准入事务继续检查取消标记；取消保护存在期间不得启动该 Task 的新执行。普通信息 Claim 和资源提议不得改变取消保护。

Suspend 最终事务必须再次确认该 Task 没有活跃 Attempt、outstanding lease 或占用的 execution slot；不满足时不得提交 SUSPENDED。最终状态变更与取消标记清除须在同一事务完成。统一状态校验必须拒绝已 SUSPENDED/CLOSED 但仍有上述执行活动、lease 或 slot 的 Task。

解锁不等于清除取消标记。取消已提交后，错误返回或持锁进程退出不得解除调度保护；后续请求不得隐式接管残留取消状态。显式 `recover` 必须核对并处理残留状态，证明执行终止和结算后才能清除保护，且不得抢占仍存活的持锁控制请求。沿用既有包结构、依赖约束、外部协议和 Git 调用预算，不新增 semantic object 或通用锁框架。

worker result 中若有 Task `fulfilled` Claim：只有 issuer actor 有 `task.complete` 时才触发同样的 qualifying close；否则只保存 informational Claim。

## 33. Option operations

实现 propose、refine、split、merge、allocate、release、comment、discuss、thread、close。所有语义变换创建新 Option；已有 Option text/provenance 不修改。

`propose`：actor 需要 `option.propose`；创建 OPEN Option。若是 Task root proposal，resource parent=Task root；若显式从同 Task 的 source Option 派生，resource parent=source Option account，semantic edge relation=`propose`。初始 allocation=0。

`refine`：actor 需要 `option.refine`；创建 OPEN child Option，semantic edge relation=`refine`，resource parent=parent Option account；资源按第 8.4 节 transfer；省略 --transfer-wall-ms 时为 0。旧 Option 保持原 status。

`split`：actor 需要 `option.split`；一次 transaction 创建 >=2 个 OPEN child Options，relation=`split`，按第 8.4 节分配真实余额；parent 不自动 close。

`merge`：actor 需要 `option.merge`；输入 >=2 个 distinct OPEN participant Options，必须同 Task；创建一个 OPEN merged Option，所有 participant -> merged edge relation=`merge`；resource parent 与 transfer 规则见第 8.4 节。participants 不自动 close。

`allocate`：actor 需要 `option.allocate`；只允许从该 Option 的 immediate resource parent 向 Option account transfer positive `wall_ms`。若 parent 余额不足 whole operation fails。v0 不实现任意 sibling-to-sibling allocator；需要重分配时先通过 close/merge 等本文定义动作回收到 parent。

`close`：actor 需要 `option.complete`。该操作等价于该 actor 发布 subject=Option、claim_type=`fulfilled` 的 qualifying Claim，然后 Option -> CLOSED，unused remaining 返回 immediate resource parent。CLOSED Option 不再 runnable，也不能 refine/split/allocate；历史与 lineage 保留。

worker result 的 Option `fulfilled` Claim 只有 issuer 同时有 `option.complete` 时触发相同 close；否则仅 informational。

任何 operation 对 CLOSED input、跨 Task resource move、重复 participant、负数/溢出 wall_ms 都 fail closed，不进行部分修改。

## 34. Review/rework

Mutation 返回 PROMOTE_FINAL 后：capture Artifact -> run protected tests -> run reviewer Attempt。

Reviewer REJECT：Artifact Pn -> fresh mutation Attempt(target=Pn) -> Artifact Pn+1。

必须创建新 Attempt、新 workspace、使用剩余 Option budget。不得 reopen Artifact；reviewer 不得直接 patch；reviewer 不得直接加测试；不得插入 Researcher 自动翻译 finding。

## 35. Completion

Promotion、protected test PASS、review APPROVE 都不等价于 Option 或 Task semantic completion。

普通 `fulfilled` Claim 默认只是信息。只有 issuer actor 持有对应 `option.complete` / `task.complete` capability 时才具有 qualifying completion effect，触发第 32/33 节固定 close 行为。

Core 不检查 role 名字 `O5`，不从自然语言、model/provider、review verdict 或 test result 推断 completion authority。

## 36. CLI contract

CLI executable 固定名 `scp` / Windows `scp.exe`。全局语法：

```text
scp [--config PATH] [--json] <command> [args...]
```

`--config` 缺省为当前目录 `scp.json`。**所有命令包括 `scp init` 都要求该配置存在且合法**；`scp init` 只根据配置创建/迁移 database、artifact store 和必要目录，不生成带隐式默认值的 `scp.json`。`--json` 只改变输出编码，不改变行为。

### 36.1 Exit codes

```text
0  success
1  unexpected internal error / panic boundary
2  CLI usage, JSON/config/schema parse error
3  domain/capability/precondition failure
4  operation blocked by unresolved scoped infrastructure blocker
5  STORAGE_FAILURE / CORE_INCONSISTENT / unrecoverable infrastructure failure
```

不得把失败 command 返回 0。Human-readable output 写 stdout/stderr 均可，但 `--json` 模式下 stdout 必须只包含一个 JSON object，日志写 stderr。

`--json` success envelope：

```json
{"ok":true,"command":"task.create","data":{}}
```

failure envelope：

```json
{"ok":false,"command":"task.create","error":{"code":"STABLE_CODE","message":"human text"}}
```

`data` 不允许由各 command 自由设计；v0 使用下面冻结的 projection。所有 timestamp 必须是 UTC RFC3339 字符串；nullable 字段显式为 `null`，不得省略 required key。除下面列出的 key 外不得增加额外字段。

#### 36.1.1 Stable error codes

`error.code` 只能是以下 closed enum：

```text
INTERNAL_ERROR
USAGE_ERROR
INVALID_CONFIG
INVALID_JSON
SCHEMA_INVALID
NOT_FOUND
CAPABILITY_DENIED
PRECONDITION_FAILED
INVALID_STATE
INSUFFICIENT_RESOURCE
LIMIT_EXCEEDED
PRECONDITION_CHANGED
WORKER_UNAVAILABLE
RUNNER_UNAVAILABLE
REPOSITORY_UNAVAILABLE
STORAGE_FAILURE
CORE_INCONSISTENT
BLOCKED
```

message 只用于人读，不得被 Core 再解析为控制信号。Error code -> process exit code 固定映射：`INTERNAL_ERROR -> 1`；`USAGE_ERROR|INVALID_CONFIG|INVALID_JSON|SCHEMA_INVALID -> 2`；`NOT_FOUND|CAPABILITY_DENIED|PRECONDITION_FAILED|INVALID_STATE|INSUFFICIENT_RESOURCE|LIMIT_EXCEEDED|PRECONDITION_CHANGED -> 3`；`WORKER_UNAVAILABLE|RUNNER_UNAVAILABLE|REPOSITORY_UNAVAILABLE|BLOCKED -> 4`；`STORAGE_FAILURE|CORE_INCONSISTENT -> 5`。

#### 36.1.2 Frozen JSON object projections

`TaskJSON`：

```json
{
  "id":"...","objective":"...","repo_path":"...","repo_ref":"...",
  "responsible_actor_id":"...","status":"ACTIVE|SUSPENDED|CLOSED",
  "current_authoritative_sha":"40-hex","state_revision":0,
  "resources":{"total_minted_wall_ms":0,"remaining_wall_ms":0,"outstanding_lease_wall_ms":0,"total_charged_wall_ms":0,"retired_wall_ms":0},
  "created_at":"RFC3339","updated_at":"RFC3339"
}
```

`TaskJSON.resources.remaining_wall_ms` 是该 Task 全部 resource accounts 当前 `remaining_wall_ms` 的总和；`outstanding_lease_wall_ms` 也是该 Task 所有 outstanding lease 的总和。这样 CLOSED Task 必须呈现 `remaining_wall_ms=0,outstanding_lease_wall_ms=0`，而未消费撤销量出现在 `retired_wall_ms`。

`OptionJSON`：

```json
{
  "id":"...","task_id":"...","text":"...","status":"OPEN|CLOSED",
  "resource_parent_id":"...","remaining_wall_ms":0,
  "created_against_repo_sha":"40-hex","created_against_state_revision":0,
  "created_by_actor":"...","created_at":"RFC3339"
}
```

`AttemptJSON`：固定包含第 7 节所有 Attempt 字段，字段名与第 7 节 snake_case 名称一致；nullable 的 `ended_at`、`exit_code`、`termination_reason`、`produced_artifact_id` 必须显式 `null`。`ArtifactJSON`、`ClaimJSON`、`BlockerJSON` 同理固定包含第 6、5、38A.4 节列出的全部 stored field，不添加 derived prose。

`StatusJSON` 固定为：

```json
{
  "fail_stop":false,
  "execution_slot":{"state":"IDLE|BUSY","owner_attempt_id":null},
  "tasks":[],
  "running_attempt":null,
  "unresolved_blockers":[]
}
```

其中 `tasks` 只包含当前 ACTIVE/SUSPENDED Task，使用 `TaskJSON` 并按 `created_at,id` 排序；`running_attempt` 使用 `AttemptJSON|null`，`unresolved_blockers` 使用 `BlockerJSON`。

#### 36.1.3 Command -> data mapping

- `task create/extend/suspend/resume/close/show` -> `TaskJSON`；
- `option show/propose/refine/merge/allocate/release/close` -> `OptionJSON`；
- `option comment` -> `ClaimJSON`；
- `option discuss` -> `{"comment":ClaimJSON,"queued":true}`（success queued 必须 exactly true）；
- `option thread` -> `{"option":OptionJSON,"messages":[ClaimJSON...]}`；
- `option split` -> `{"parent":OptionJSON,"children":[OptionJSON...]}`；
- `option/attempt/artifact/claim/blocker list` -> `{"items":[<对应 JSON type>...]}`；
- `attempt show/interrupt` -> `AttemptJSON`；
- `artifact show` -> `ArtifactJSON`；
- `artifact export` -> `{"artifact_id":"...","out":"...","sha256":"64-hex","size_bytes":0}`；
- `claim create` -> `ClaimJSON`；
- `blocker resolve` -> `BlockerJSON`；
- `status` -> `StatusJSON`；
- `init` -> `{"schema_version":0,"database":"...","artifact_store":"..."}`；
- `recover` -> `{"recovered_attempt_ids":["..."],"promotion_journal_reconciled":0,"stale_lock_removed":false}`；
- `run --json` 在进程正常退出时只输出一次 `{"stop_reason":"SIGNAL|FAIL_STOP"}`；运行期间事件只写 stderr/log，不得向 stdout 流式输出多个 JSON object。

所有 list 命令的 `items` 必须 deterministic（至少按 `created_at,id` 排序）。ID、status、amount 使用 JSON string/integer 原类型，不使用格式化字符串。

### 36.2 Commands

固定实现以下命令；不得为了 v0 增加另一套同义 command surface：

```text
scp init
scp run
scp recover
scp status

scp task create --objective TEXT --repo PATH --ref REF --responsible-actor ID --wall-ms N
scp task extend TASK_ID --wall-ms N
scp task suspend TASK_ID
scp task resume TASK_ID
scp task close TASK_ID
scp task show TASK_ID

scp option list --task TASK_ID [--status OPEN|CLOSED]
scp option show OPTION_ID
scp option propose --task TASK_ID --text TEXT [--source OPTION_ID]
scp option refine OPTION_ID --text TEXT [--transfer-wall-ms N]
scp option split OPTION_ID --spec FILE
scp option merge --task TASK_ID --spec FILE
scp option allocate OPTION_ID --wall-ms N
scp option release OPTION_ID
scp option comment OPTION_ID --text TEXT
scp option discuss OPTION_ID --text TEXT
scp option thread OPTION_ID
scp option close OPTION_ID

scp attempt list [--task TASK_ID]
scp attempt show ATTEMPT_ID
scp attempt interrupt ATTEMPT_ID

scp artifact list [--task TASK_ID]
scp artifact show ARTIFACT_ID
scp artifact export ARTIFACT_ID --out PATH

scp claim list [--task TASK_ID] [--subject-type TASK|OPTION|ARTIFACT] [--subject-id ID]
scp claim create --task TASK_ID --subject-type TASK|OPTION|ARTIFACT --subject-id ID --type TYPE [--payload FILE]

scp blocker list [--unresolved]
scp blocker resolve BLOCKER_ID
```

`option split --spec FILE` schema：

```json
{"children":[{"text":"...","wall_ms":1000},{"text":"...","wall_ms":2000}]}
```

至少 2 children；unknown fields invalid。

`option merge --spec FILE` schema：

```json
{
  "text":"merged option text",
  "participants":[
    {"option_id":"...","transfer_wall_ms":1000},
    {"option_id":"...","transfer_wall_ms":500}
  ]
}
```

至少 2 distinct participants；全部属于 `--task`；unknown fields invalid。

`claim create --payload FILE`：FILE 内容必须为 JSON object；省略时 payload=`{}`。CLI operator actor 由 config 的 `operator_actor_card` 固定，命令不得通过 `--actor` 自选 identity。`claim create` 需要 `claim.publish`；若 type=`fulfilled` 且 operator 还具有对应 `option.complete`/`task.complete`，则在同一 Core transaction 中执行 qualifying close；否则仅保存 informational Claim。

`scp status` 必须至少输出：global fail-stop state、execution slot owner/idle、ACTIVE/SUSPENDED Task 概览、current RUNNING Attempt、unresolved blockers；human mode 中 unresolved blocker 必须显眼。

所有 list 命令必须 deterministic（至少按 `created_at,id` 排序），不得依赖 SQLite 未定义 row order。

## 37. Scheduler mechanical state machine

`scp run`：acquire scheduler lock；循环 recover/verify no dangling Attempt；derive next runnable operation from persistent Core state；execute exactly one mechanical step/chain；repeat。v0 不存在 model-decided workflow planning。

Scheduler lock 使用 atomic creation of `run.lock` directory。正常退出删除。发现 stale lock：拒绝启动并提示运行 `scp recover`。不要设计 distributed lock。

### 37.1 Runnable definition and precedence

某个 operation runnable 当且仅当：其 Task=ACTIVE；相关 Option（如有）=OPEN；所需 resource anchor 有足够 positive balance 形成本步 lease；相关 scope 无 unresolved blocker；global fail-stop=false；required authoritative resource/precondition 当前满足。Option mutation 需要既有 Pending 且 Option account 有余额；discussion 使用 Task root，允许 Option allocation=0。仅 funded 不产生 Pending。

全局只允许一个 execution slot。选择优先级固定为：

1. 已存在且可执行的 Pending chain/discussion next step（包括 host release 创建的 initial mutation）；
2. ACTIVE Task 的一次性 initial exploration；
3. idle。

不得扫描 OPEN/funded Options 并构造 mutation。No Option can start a mutation chain without explicit host option.release.

若 pending next step 只因余额不足或 unresolved scoped blocker 暂时不可执行，release execution slot，但保留该 pending step；allocation 增加或 blocker 显式 resolve 后，从**同一个 next step/Artifact target**继续，不得悄悄改成新的 authoritative-state mutation。Task suspend/close 则取消该 Task 的 pending chain，历史 Artifact/Claim 保留。

### 37.2 One-time initial exploration

每个新 Task 在 create transaction 中记录一次 `initial_exploration_pending` 内部 runtime state（它不是 semantic object）。第一次 `scp run` 调度该 Task 时：

1. 读取 `scp.json.exploration.initial_option_generation_attempts = N`，顺序执行 exactly `N` 个 **fresh** `option_generation` Attempts，全部 resource anchor=Task root；v0 全局串行意味着它们不并发；
2. 每个合法 result 的 options 追加到同一个 raw Option batch B；某次 invalid/crash/timeout 只贡献 0 个 Option，该次已消费且不自动重试；
3. N 次全部结束后，若 `|B| >= 2`，exactly one `merge_judge` 对整个 B 分组；invalid partition -> dedup chain 结束，raw B 保留；
4. 对 verified partition，按 MergeJudge 输出 group 顺序处理：size=1 的 group 直接以该 raw Option 作为 canonical working option，不调用 MergeSynth；size>=2 的 group exactly one `merge_synth`，按第 14.5 节创建 zero-allocation merged Option；单个 synth invalid 只影响该 group；
5. initial exploration 完成后该 marker 永久结束，不因重启再次运行。`N` 只决定独立 fresh generation Attempt 次数，不 mint 资源；每次 Attempt 都受 Task root 余额和 role-card lease limit 约束。

CLI `option propose` 创建的后续人工 Option **不会自动重新触发全 Task dedup**；v0 不提供持续后台 dedup。它们是 inert candidates；allocation 后仍需 host option release。

Initial exploration/dedup 不自动分配 Option budget。完成后 scheduler idle/sleep，无论 Option 是否 funded；等待 operator `option release` 或 `option discuss` 创建 Pending。

### 37.3 Mutation/review/promotion transition table

下表是 normative；不得自行插入 planner/workflow node：

| Current condition / result | Mechanical next step | Chain semantics |
| --- | --- | --- |
| explicit host option release succeeds, no pending chain | `mutation(target=Option, base=current authoritative SHA)` | start chain |
| mutation returns `CONTINUE_FINAL` | capture Artifact A -> next `mutation(target=A)` | retain chain; fresh workspace from A |
| mutation returns `PROMOTE_FINAL` | capture Artifact A -> protected test(A) | retain chain |
| mutation returns `DROP_FINAL`, missing/invalid result, ordinary CRASHED/TIMED_OUT/INTERRUPTED | capture writable Artifact if possible -> no next step | end chain; Option remains OPEN; no automatic retry; another cycle requires host option release |
| protected test completes normally, exit=0 | reviewer(A, test=PASS) | retain chain |
| protected test completes normally with nonzero exit or test timeout | reviewer(A, test=FAIL/TIMEOUT) | retain chain; Artifact is mechanically non-promotable in this cycle |
| protected test runner infrastructure failure | create blocker per §38A | preserve pending test/review chain; release slot until explicit resolve |
| review result invalid | treat as `REJECT` | same as reject |
| reviewer `REJECT` | fresh `mutation(target=A)` rework | retain chain; rejection/findings supplied as facts |
| reviewer `APPROVE` and protected test=PASS | promotion(A) | retain chain |
| reviewer `APPROVE` but protected test!=PASS | fresh `mutation(target=A)` rework | effective mechanical reject reason=`PROTECTED_TEST_FAILED`; reviewer approval cannot override protected runner failure |
| promotion CAS succeeds | update journal/APPLIED; Task authoritative SHA=new SHA | end chain; Option remains OPEN; Pending deleted; idle until another explicit release; promotion != completion |
| promotion precondition changed | record `PRECONDITION_CHANGED`; no repo write | end chain; Option OPEN; another explicit release required; future work uses current authoritative state |
| any next step lacks enough resource | no execution | release slot; retain exact pending step until explicit allocation/extend or Task suspend/close |
| scoped infrastructure blocker during worker/review/promotion | record blocker | release slot; retain exact pending step until explicit resolve unless Task suspended/closed |

A reviewer is always given the protected test result. Normal test failure is **not** an infrastructure blocker. Promotion requires the conjunction `protected_test == PASS && review == APPROVE && CAS precondition holds`.

After successful promotion, Pending is deleted. Option remains OPEN and funded but idle; another mutation cycle requires a new explicit host `option release`. Review APPROVE only authorizes promotion of the current Artifact.

### 37.4 Chain cancellation

`task suspend` / `task close` 按第 32 节取消该 Task 的 active or pending chain。`attempt interrupt` 只终止当前 Attempt，并按默认 DROP semantics 结束当前 chain step；它不 close Option。取消/interrupt 不能删除已捕获 Artifact 或改写 Claims/created_against。

## 38. Crash recovery

scp recover 固定执行：

1. terminate SCP-Worker distro
2. inspect DB for non-terminal Attempt
3. if writable workspace exists: capture Artifact
4. mark Attempt CRASHED
5. conservatively charge full outstanding lease
6. inspect repo_update_journal
7. reconcile Git refs
8. clean temporary files
9. remove stale run.lock

不得猜测 worker 是否“其实已经完成”。

## 38A. 基础设施失败、阻塞与状态冲突

本节是 v0 固定失败模型。不得继续扩展 error taxonomy，除非某个新类别会导致 Core 采取不同的机械动作。

### 38A.1 正常 Attempt / operation 终止

以下属于系统正常处理范围，不是基础设施失败：

- RETURNED
- CRASHED
- TIMED_OUT
- INTERRUPTED
- INVALID_OUTPUT
- LIMIT_EXCEEDED
- PRECONDITION_CHANGED

这些情况不得自动升级成 infrastructure blocker。

`PRECONDITION_CHANGED` 表示操作所依赖的 world state 已变化。例如 Promotion 时 Artifact 的 `base_repo_sha = A`，但 authoritative ref 已变为 B。此时：

- 保留 Artifact；
- Option 保持原状态；
- 不修改 repository；
- 不判定 worker 失败；
- 不判定 Artifact 被 review reject；
- 不自动 merge/rebase；
- 记录 operation/audit 结果为 PRECONDITION_CHANGED。

Repository drift/conflict 本身不是 infrastructure failure。现实状态变化不等于系统故障。

### 38A.2 Infrastructure blocker

只实现以下三类：

1. `WORKER_UNAVAILABLE`
   - configured worker 无法承担工作；
   - 例如 worker wrapper 检测到 auth/session/subscription/runtime 不可用，或 worker executable 缺失；
   - Core 不识别 Codex、DeepSeek、auth 等 provider 概念；
   - worker profile 可配置一个或多个 reserved unavailable exit code，由 wrapper 将 provider-specific failure 映射成该 exit code；
   - Core 只看到 `WORKER_UNAVAILABLE`。

2. `RUNNER_UNAVAILABLE`
   - WSL distro 不存在、损坏、无法启动、无法进入或无法可靠 terminate；
   - 该 blocker 阻止所有依赖该 runner 的 Attempt。

3. `REPOSITORY_UNAVAILABLE`
   - repository path 不可访问；
   - required ref 无法解析且按当前 Task 配置应存在；
   - Git mechanism 自身无法执行或超时；
   - repository/object I/O/corruption 导致 Core 无法读取 authoritative state。

基础设施 blocker 的共同规则：

- 当前 Attempt 若已创建，则结束为 `TERMINATED`；
- `termination_reason` 写入对应类型；
- 不把 blocker 解释为 Option/Task/Artifact 的语义失败；
- 不关闭 Option；
- 不自动 retry；
- 立即创建 persistent runtime blocker；
- `scp status` 上报 responsible O5/operator；
- blocker 被显式 resolve 前，scheduler 不得再次调度受影响对象。

如果 worker 根本未开始执行，则只收取实际已经消耗的 wall time；不得因为 `WORKER_UNAVAILABLE` 自动收完整 lease。

### 38A.3 Global fail-stop

只实现以下两类：

1. `STORAGE_FAILURE`
   - SQLite 持久化失败；
   - Artifact store 无法写入/fsync/atomic rename；
   - disk full / persistent filesystem I/O failure；
   - Core 无法可靠保存 authority state。

2. `CORE_INCONSISTENT`
   - frozen invariant 被检测为破坏；
   - recovery journal 与实际 authoritative state 无法解释；
   - 出现按状态机定义不可能出现的状态。

Global fail-stop 的固定行为：

- 不再启动任何新 Attempt；
- 不做自动修复；
- 尽可能停止当前 worker/runner；
- 输出明确诊断并上报 O5/operator；
- 只有人工处理并显式恢复后才能继续。

### 38A.4 Blocker persistence

增加一个内部运行表 `runtime_blockers`。它不是 SCP semantic object。至少保存：

- id
- kind: WORKER_UNAVAILABLE / RUNNER_UNAVAILABLE / REPOSITORY_UNAVAILABLE / STORAGE_FAILURE / CORE_INCONSISTENT
- scope: WORKER_PROFILE / RUNNER / TASK / GLOBAL
- subject_id nullable
- message
- created_at
- resolved_at nullable

Scheduler 启动任何 Attempt 前必须检查 unresolved blocker：

- WORKER_PROFILE：跳过该 profile；
- RUNNER：不启动该 runner 上的 Attempt；
- TASK：不调度该 Task；
- GLOBAL：整个 scheduler fail-stop。

`scp blocker resolve <id>` 只解除 blocker，不负责 provider-specific 修复。O5/operator 必须先完成实际修复（例如重新登录 worker），再显式 resolve。

### 38A.5 Worker unavailable signaling

Core 不解析 provider 错误文本。Worker profile 可配置：

```json
{
  "unavailable_exit_codes": [75]
}
```

wrapper/executable 若知道自身基础设施不可用，可使用 reserved exit code 退出。Core 将其机械映射为 `WORKER_UNAVAILABLE`。普通非零 exit code 仍然只是 `CRASHED`，不得自动升级为 blocker。

不得让 Core 自动执行 login、credential refresh 或 provider-specific recovery。

## 39. boundedexec

所有外部 process（git、wsl、test command、worker、tar/helper）必须通过统一 bounded execution helper。每次执行必须显式指定 timeout、max stdout、max stderr。输出达到 cap：停止保留额外输出或按操作策略终止。不得允许内存无限积累 subprocess output。

## 40. 文件系统限制

所有递归扫描必须有 max file count、max total bytes、max single file size、max path length。这些值来自 scp.json。生产配置不得依赖代码里的隐式 unlimited default。越界时 operation fails closed。

## 41. 配置文件：scp.json / scp.example.json

`scp.example.json` 必须完整展示 v0 schema；生产 `scp.json` 使用相同 schema。unknown top-level/nested field 一律拒绝，防止拼写错误被静默忽略。

固定 schema shape：

```json
{
  "schema_version": 0,
  "database": ".local/example/scp.db",
  "artifact_store": ".local/example/artifacts",
  "operator_actor_card": "O5-1",
  "role_cards": {
    "O5-1": "testdata/cards/O5-1.json",
    "operator": "testdata/cards/operator.json",
    "reviewer": "testdata/cards/reviewer.json",
    "merge": "testdata/cards/merge.json",
    "discussion": "testdata/cards/discussion.json"
  },
  "wsl": {
    "distro": "SCP-Worker",
    "test_distro": "SCP-Test",
    "attempt_root": "/scp/attempt"
  },
  "exploration": {
    "initial_option_generation_attempts": 1
  },
  "limits": {
    "workspace_max_files": 10000,
    "workspace_max_bytes": 104857600,
    "single_file_max_bytes": 16777216,
    "path_max_bytes": 240,
    "stdout_max_bytes": 1048576,
    "stderr_max_bytes": 1048576,
    "result_max_bytes": 1048576,
    "git_export_max_bytes": 134217728,
    "external_process_timeout_ms": 60000
  },
  "protected_test": {
    "command": [
      "go",
      "test",
      "./..."
    ],
    "timeout_ms": 60000,
    "output_limit_bytes": 1048576
  },
  "workers": [
    {
      "id": "operator",
      "command": [
        "/opt/scp-workers/operator"
      ],
      "actor_card": "operator",
      "timeout_ms": 60000,
      "workspace": "writable",
      "synthetic_git": true,
      "unavailable_exit_codes": [
        75
      ]
    },
    {
      "id": "reviewer",
      "command": [
        "/opt/scp-workers/reviewer"
      ],
      "actor_card": "reviewer",
      "timeout_ms": 60000,
      "workspace": "readonly",
      "synthetic_git": false,
      "unavailable_exit_codes": [
        75
      ]
    },
    {
      "id": "merge",
      "command": [
        "/opt/scp-workers/merge"
      ],
      "actor_card": "merge",
      "timeout_ms": 60000,
      "workspace": "none",
      "synthetic_git": false,
      "unavailable_exit_codes": [
        75
      ]
    },
    {
      "id": "discussion",
      "command": [
        "/opt/scp-workers/discussion"
      ],
      "actor_card": "discussion",
      "timeout_ms": 60000,
      "workspace": "readonly",
      "synthetic_git": false,
      "unavailable_exit_codes": [
        75
      ]
    }
  ],
  "operation_profiles": {
    "mutation": "operator",
    "review": "reviewer",
    "option_generation": "merge",
    "merge_judge": "merge",
    "merge_synth": "merge",
    "discussion": "discussion"
  }
}
```

Normative validation：

- `schema_version` 必须 exactly 0；
- database/artifact_store 必须非空 host path；
- `operator_actor_card` 必须存在于 `role_cards`；
- role card 文件必须存在并满足 v0 role-card schema；
- distro 必须非空且 v0 production 要求 `SCP-Worker`；
- `exploration.initial_option_generation_attempts` 必须是 positive integer；每个 Task initial exploration exactly 执行这么多个 fresh `option_generation` Attempts；
- 所有 critical numeric limit/timeout 必须 >0，0/缺失不得解释为 unlimited；
- worker id 唯一，command 非空，actor_card 必须存在；
- workspace 只允许 `none|readonly|writable`；
- unavailable exit code 必须 1..255，且同 profile 内唯一；
- `operation_profiles` 必须恰好包含上面六个 operation key，并引用存在的 worker；
- mutation profile 必须 writable；review/discussion profile 必须 readonly；discussion synthetic_git 必须 false；merge/option generation profile 必须 none；
- protected test command 非空且 timeout/output limit >0。

示例中的具体 limit 数值不是产品默认值；生产配置必须显式包含它们。Core 不提供隐式 unlimited/default budget。

## 42. WSL provisioning

scripts/setup-worker-wsl.ps1 只负责 verify WSL2、verify dedicated distro exists、configure /etc/wsl.conf、create /scp、create worker user、create /opt/scp-workers、verify automount disabled、verify interop disabled。

不要自动修改用户已有 distro。若需要创建 distro，使用明确的 WSL import/install 步骤。不要 clone 用户开发 distro 作为生产 worker environment。

## 43. Fake workers

任何真实 agent 之前，实现 success-worker、crash-worker、timeout-worker、invalid-json-worker、huge-output-worker、workspace-writer、malicious-git-worker、worker-unavailable、fake-reviewer-approve、fake-reviewer-reject。全部作为 shell/Go 小程序放入 testdata/workers/。系统测试首先使用 fake workers。

## 44. 必须实现的测试

Data model：immutable Option、immutable Artifact、created_against retention、repo unique active binding。

Ledger property tests：随机 allocate/split/merge/close/attempt reserve-refund；始终验证 resource conservation 和 remaining >= 0。

Attempt tests：normal return、nonzero exit、timeout、interrupt、Core restart、missing result、invalid result。

Infrastructure failure tests：

- reserved unavailable exit code -> WORKER_UNAVAILABLE；
- WORKER_UNAVAILABLE 创建 profile blocker，Option 保持 open，且 scheduler 不自动 retry；
- O5/operator 修复后显式 blocker resolve，调度才恢复；
- WSL start/terminate failure -> RUNNER_UNAVAILABLE；
- repo missing/Git mechanism failure -> REPOSITORY_UNAVAILABLE；
- authoritative ref drift at promotion -> PRECONDITION_CHANGED，不能生成 infrastructure blocker；
- SQLite/Artifact durable write failure -> STORAGE_FAILURE + global fail-stop；
- synthetic invariant violation -> CORE_INCONSISTENT + global fail-stop。

Artifact tests：normal files、large files、file-count overflow、byte overflow、partial capture failure、atomic publication、unsupported special file。

Git tests：创建 1/100/10000 commits、many branches 的 synthetic repositories；要求 SCP 的 Git call count 不随 history 长度增长；确认 git log / rev-list / blame 永远没有被调用。

R4 regression：root/nested `.gitattributes` 中的 export-ignore / export-subst、取消/恢复 unspecified/显式 value、macro、comment 中的保留标识符均拒绝；无相关标识符或无 `.gitattributes` 的正常仓库通过。验证 mutable worktree/index、原仓库 info/attributes、global/system 属性或用户配置不能影响 archive；unchanged snapshot → promotion round-trip 不改变树内容；拒绝时不运行 archive；机械检查 `export_attr_inspection <= 1 && export_tree <= 1`。

Synthetic worker Git test：authoritative repo 10000 commits，worker workspace 执行 git rev-list --count HEAD，结果必须为 1。

Promotion tests：normal CAS、target drift、Core crash before CAS、Core crash after CAS before DB update、recovery。

Review tests：reviewer cannot change Artifact；reject creates fresh Attempt；old Artifact remains unchanged。

Interrupt tests：worker 创建 child process 无限循环；interrupt 后验证 WSL distro terminated、child gone、Attempt terminal、Artifact captured、Option remains OPEN。

Suspend/resume：T1 ACTIVE -> T1 suspend -> T2 bind same repo -> T2 close -> T1 resume；历史 created_against 不变化。

R1b regression：确定性交错覆盖 suspend 与 `option close`、suspend 与 qualifying Option `fulfilled`，包括取消已提交、执行已结算但最终 lifecycle transaction 尚未提交的窗口；不得依赖随机 sleep。跨 CLI 进程竞争必须返回 `BLOCKED`；持锁等待不妨碍已有执行结算；取消后错误返回/异常退出保持保护，显式 recover 可恢复且不能抢占活控制请求。验证 SUSPENDED/CLOSED Task 无活跃 Attempt、outstanding lease 或占用 slot 的统一不变式，并保留原有回归和断言。

Role-card tests：`scp_harness_role_card_v0.schema.json` valid fixtures 全通过；unknown context/capability/limit、wrong scope、duplicate capability name、missing `lease.wall_ms` 全拒绝；改变 `influence` 不得改变 v0 Core decision。

Scheduler transition tests：第 37.3 表每一行至少一个测试；尤其 test FAIL + reviewer APPROVE 仍不得 promotion、resource/blocker pause 后从同一 pending step 恢复、promotion 后 Option 保持 OPEN。

CLI JSON golden tests：第 36.1 冻结 projection/command mapping 与 stable error code 全覆盖；unknown output field 视为实现 bug。

Task-close ledger test：close 前所有 outstanding lease 已 settle，全部 remaining 原子归零并进入 `retired_wall_ms`，conservation equation 在 CLOSED Task 上继续成立。

Claim 保留值测试：精确 `resource.propose` + 双 capability 成功；分别缺少 `claim.publish` 或 `resource.propose` 时拒绝且不降级；大小写变体、前缀扩展、payload 资源描述不触发保留语义；proposal 创建前后完整 ResourceLedger 不变；`fulfilled` 的 informational 与 qualifying Option/Task completion 行为不受影响。CLI 与 worker Claim 路径都必须应用该规则。

A10：`scp_harness_v0_walkthrough.md` 的所有 checkpoint 全自动验证。

## 45. CI / architecture constraints

最终仓库必须提供可由 `go test ./...` 或独立 test command 机械执行的 architecture checks。至少验证：

1. 除 `internal/boundedexec` 外，生产 Go 源码不得直接 import/use `os/exec` 启动外部 process；
2. 所有 Core Git command construction 只存在于 `internal/gitrepo`；
3. production Core 不出现 forbidden Git command strings/argv construction；
4. `go.mod` 的直接第三方 dependency 只有 `modernc.org/sqlite`；
5. `internal/` package 集合不超出第 3 节 allowlist；
6. production import 不得包含 provider/model SDK、Agent SDK、workflow engine、Docker/K8s client、ORM、vector DB/RAG framework；
7. scheduler/worker 路径不得存在同时启动两个 execution activity 的 worker pool/errgroup/parallel dispatch；必要的单 process I/O goroutine 不因此被禁止；
8. protected WSL integration test 不得因为“当前不是 CI/没有 WSL”被静默 `Skip` 后计为 acceptance PASS；FINAL ACCEPTANCE 必须在真实 Windows 11 + WSL2 `SCP-Worker` 环境运行；
9. `scp.example.json` 必须被同一 production config parser 成功读取；故意加入 unknown field 的 fixture 必须被拒绝；
10. strict result schema 的 unknown/duplicate/missing-field fixtures 必须被拒绝；
11. `docs/spec/` 中五个 frozen authority/spec 文件必须存在于最终 source repository，role-card parser 行为必须与 `scp_harness_role_card_v0.schema.json` 一致。

CI 最低命令：

```text
go test ./...
go vet ./...
```

如果普通云 CI 无法提供 WSL2，可以把 WSL acceptance 标记为“not run in this CI environment”，但这不等于 Final Acceptance；最终交付前必须在目标 host 实跑。

## 46. 推荐 implementation milestones 与 Final Acceptance Cases

本节只帮助 autonomous implementer 分解问题。**不是 Gate，不要求顺序，不要求中间 commit，不允许因为某个 Acceptance Case 暂时失败就停止整个 bootstrap。**

### Phase 0 — 环境与风险 spike

建议尽早验证：
1. Go Windows executable 能启动 `wsl -d SCP-Worker -- <fake worker>`；
2. 能把文件 stream 到 WSL；
3. 能从 WSL stream 文件回来；
4. `wsl --terminate SCP-Worker` 能杀掉 child/grandchild；
5. Windows Git rev-parse/archive/update-ref CAS 在 test repo 正常工作；
6. 10000 commit test repo 中 Git export 不进行历史业务查询。

**Acceptance A0**：上述全部最终自动化测试通过。

### Phase 1 — Skeleton + SQLite

建议实现 config、schema migration、Task、Option、Claim、Artifact metadata、Attempt、resource account、audit log、SQLite transactions。ID 使用 `crypto/rand` 128-bit hex，不增加 UUID dependency。

**Acceptance A1**：`scp init / task create / task show` 工作，进程重启后数据正确，strict config/schema 生效。

### Phase 2 — Generic worker execution

建议实现 WorkerProfile、input/result strict schemas、bounded stdout/stderr、timeout、WSL execution。使用 fake workers，不接真实 provider protocol。

**Acceptance A2**：全部 fake worker lifecycle/result-schema/capability tests 通过。

### Phase 3 — Artifact

建议实现 workspace materialization、capture、tar、SHA-256、atomic publish、continuation。

**Acceptance A3**：base files -> worker edits -> worker crash -> Artifact captured -> fresh workspace restored；Artifact immutable。

### Phase 4 — Bounded Git

建议只实现 resolve_ref、export_tree、promote、synthetic one-commit worker repo，并加入 forbidden Git architecture tests。

**Acceptance A4**：10000 commit authoritative repo 的 Attempt startup Core Git subprocess count 与 1 commit repo 相同；worker synthetic history 长度 exactly 1；worker 可在 synthetic repo 自行使用普通 Git 而看不到 authoritative history。

### Phase 5 — Ledger + Option lifecycle

建议实现 create/extend/allocation/refine/split/merge/close/lease/refund 和 property tests。

**Acceptance A5**：大量随机操作中 conservation equation 始终成立，无负余额、无非 create/extend mint、无丢失预算；merge LCA/explicit transfer 与 rollback 行为精确满足第 8 节。

### Phase 6 — Scheduler

实现 global execution slot、Pending-first deterministic scheduling、runnable check、resource lease、worker execution。

**Acceptance A6**：多个 Option 可连续自动执行；任意时刻 execution activity <=1；interrupt 可 out-of-band 终止当前 Attempt，但下一 activity 必须等待 slot release。

### Phase 7 — Mutation / Test / Review / Promotion

建议完成 Option -> mutation -> Artifact -> protected test -> reviewer -> reject/rework 或 approve -> promotion，以及 promotion journal/recovery。

**Acceptance A7**：至少两个 E2E：A mutation -> approve -> promote；B mutation -> reject -> fresh rework -> approve -> promote；invalid review 必须保守阻止 promotion。

### Phase 8 — Option generation / dedup

建议实现 raw proposal、MergeJudge、partition validation、MergeSynth、merged Option。

**Acceptance A8**：duplicate ID、missing ID、unknown ID、cross-Task ID 全部导致 partition 被 Core 拒绝；Raw Options 永远仍可读取；merged Option 初始不凭空获得 budget。

### Phase 9 — Suspend / Resume / Interrupt / Recovery

建议实现 interrupt、Task suspend/resume/close、scheduler crash recovery、promotion crash recovery。

**Acceptance A9**：T1 active -> worker running -> interrupt -> T1 suspend -> T2 bind/run/close -> T1 resume；旧 history/created_against 不重写，资源无 mint/loss。

### Phase 10 — Frozen walkthrough

按照 `scp_harness_v0_walkthrough.md` 的 frozen Vorton fixture 完整模拟。该 fixture 的 symbolic IDs、fake-worker result sequence、resource transfers、repo checkpoints、interrupt/suspend/resume 顺序和最终 ledger equations 都是 normative；实现者只能决定测试 harness 如何驱动它，不能改 scenario 让实现更容易通过。

**Acceptance A10**：fixture 全自动 integration test 逐 checkpoint 通过，且过程中所有前述 invariant/architecture checks 同时保持。真实 wall-clock charge 可以由运行时测得，但必须满足 fixture 中冻结的等式/上下界；不得用硬编码假 charge 绕过 production metering。

## 47. 最终实现禁止行为

下列内容不得出现在最终 production implementation；若 autonomous implementer 中途尝试了这些方向，应自行丢弃/重做，而不是停止整个任务等待批准：

- generic provider API；
- Codex/DeepSeek/OpenAI provider protocol integration；
- Agent SDK / MCP integration；
- Docker/Kubernetes；
- workflow engine；
- ORM / repository abstraction；
- event sourcing framework / CQRS；
- generic sandbox/storage/provider/plugin abstraction；
- parallel worker / remote execution；
- Web UI；
- Git history scanning for Core business logic；
- automatic merge/rebase/fetch/pull；
- agent memory；
- vector DB / RAG。

这些均不属于当前 v0。实现过程中如果某条路需要它们才能成立，应换更小的实现，而不是扩大产品范围。

## 48. Agent 编码规则

1. 每个 package 解决一个具体问题。
2. 不为单个实现建立 interface。
3. 不为“未来可能”增加字段。
4. 不提前泛化。
5. 不引入 pattern 仅为了 architecture cleanliness。
6. 优先普通 struct + function。
7. SQL 显式写。
8. state transition 集中在 Core。
9. external process 全部 bounded。
10. error 必须保留操作上下文。
11. fail closed。
12. invalid worker output 不改变 authority state。
13. worker exit success 不代表 semantic success。
14. 所有实际 authority mutation 由 Core 执行。

## 49. Autonomous bootstrap implementation rules

不存在每 Phase commit、人工 approval 或中间 Gate。

实现者应从空仓库连续工作到 FINAL ACCEPTANCE，可以自由组织 commit/history。最终只要求 repository 自洽、测试完整、规格满足。

实现者可以修改自己写的测试，但不能通过以下方式宣称完成：

- 删除本规格要求的 acceptance coverage；
- 把真实 integration test 改成永远 pass 的 mock；
- `Skip` 目标环境测试并把 skipped 当 PASS；
- 放宽 production invariant 以匹配错误实现；
- 只测试 happy path 而忽略本文明确的 failure/recovery case；
- 在测试中使用与 production 不同的 authority/resource semantics。

最终 acceptance 的判断对象是本文 + 运行结果，不是实现者自己的测试数量或 commit message。

## 50. 最终 Definition of Done

Final DoD 是 conjunctive：下面每一项都必须成立。任何一项未满足都不能报告 implementation complete。

### Specification closure
- 第 0 节 closed-world/fail-closed rule 已落实。
- input/result/config/CLI/capability/resource/Git/WSL contracts 与本文一致。
- 没有产品语义依赖“实现者猜测”。
- 没有未声明的 provider/dependency/package/parallelism。

### Core
- 五种 semantic object 正常持久化。
- Attempt 仍为薄 runtime record。
- Task/Option lifecycle 工作。
- capability 正确应用。
- created_against 正确保留。

### Resources
- budget 只可由 Task create/extend mint。
- conservation equation 在随机 property test 中始终成立。
- split/merge/close/allocate/lease/refund 守恒。
- merge 使用 resource-tree LCA + explicit transfer，失败原子 rollback。
- Task close 把所有未消费余额一次性转入 `retired_wall_ms`，CLOSED Task 仍满足守恒式。
- worker 不能创建 budget。
- Attempt 超时不能无限执行。

### Worker
- Core 对 worker 类型零感知。
- 任意 executable 能作为 worker。
- worker 无需 SDK。
- worker 无需 RPC。
- worker 无法直接修改 Core state。

### Artifact
- writable Attempt 总能在结束时尝试捕获。
- Artifact immutable。
- crash/interrupt Artifact 可保留。
- continuation 从 fresh workspace 开始。

### Git
- Core 不使用 Git 保存 SCP history。
- worker 看不到 authoritative Git history。
- synthetic repo 最多一个 base commit。
- Git 调用有 timeout/output cap。
- 无 Git history traversal。
- Promotion 使用 CAS。

### Review
- reviewer 无法修改 submitted Artifact。
- reject 生成 fresh Attempt。
- protected tests 不能被 candidate 改 runner policy。

### Runtime
- global execution activity 永远 <= 1；interrupt 仅作为 out-of-band control。
- Scheduler next-step 行为逐项满足第 37.3 transition table；test failure 不可被 reviewer approval 绕过。
- resource/blocker pause 后只从冻结的 pending step 恢复，不可静默换 target。
- WSL distro 可强制 terminate。
- Core crash 可以 recover。
- pending promotion 可以 recover。
- infrastructure blocker 不自动 retry。
- WORKER_UNAVAILABLE/RUNNER_UNAVAILABLE/REPOSITORY_UNAVAILABLE 会持久阻塞对应 scope 并上报 O5/operator。
- STORAGE_FAILURE/CORE_INCONSISTENT 会触发 global fail-stop。
- repository ref drift 只产生 PRECONDITION_CHANGED，不得误报为 infrastructure failure。

### Acceptance
- role-card JSON Schema、CLI JSON projection、scheduler transition table 全部有机械测试。
- Acceptance A0-A10 全部实跑 PASS。
- `go test ./...`、`go vet ./...` PASS。
- Windows 11 + dedicated WSL2 的真实 integration acceptance PASS，不以 skip 代替。
- 无 production TODO/FIXME、temporary bypass、known failing normative case。

### Complexity
生产代码中不得出现 provider-specific code、agent framework、workflow framework、distributed infrastructure。

如果实现明显膨胀，应先检查是否违反本计划的 complexity constraints。

## 51. 完成后交付物

**完整源码仓库是主要交付物，`scp.exe` 只是该源码在最终 HEAD 上构建出的派生产物。不得只交二进制、摘录、patch、代码统计或测试报告后丢弃实现源码。**

最终交付必须包含同一最终 HEAD 下的完整、可审计、可继续开发的 source repository，至少包括：

- 所有 production Go source；
- `go.mod`、`go.sum` 及全部 build metadata；
- migrations / schema 初始化文件；
- `scripts/` 下 provisioning、build、test 所需脚本；
- `scp.example.json` 及其他纳入规格的示例/静态配置；
- `docs/worker-protocol.md`、`docs/operations.md` 以及实现产生的必要维护文档；
- `docs/spec/` 下冻结规格包：`scp_harness_v0_final.md`、`scp_harness_v0_execution_plan.md`、`scp_harness_role_card_v0.md`、`scp_harness_role_card_v0.schema.json`、`scp_harness_v0_walkthrough.md`；
- `testdata/`、fake workers、fixtures；
- 完整 unit/property/integration/architecture/acceptance test suite；
- CI / architecture-check source；
- 构建 `scp.exe` 所需且属于本项目的全部源码和资源。

同时交付：

- 从上述最终 source repository 构建出的 `scp.exe`；
- Final Acceptance report。

### 51.1 Source auditability

源码交付必须满足：

1. 审计者仅凭交付 repository + 文档化的外部工具链即可重新执行 `go test ./...`、`go vet ./...`、architecture tests 和 build；不得依赖实现者机器上未交付的私有源码、临时生成文件或手工补丁。
2. production behavior 不得隐藏在未交付的 generator output、外部私有 binary、个人目录脚本或测试专用替身中。
3. 若 repository 中包含生成代码，必须同时交付其生成输入和生成方法；若生成器属于项目本身，也必须交付生成器源码。
4. `scp.exe` 必须对应 Final Acceptance report 所记录的 exact HEAD；报告记录构建命令及产物 SHA-256，使二进制可与被审计源码对应。
5. 不得在交付前删除“看起来不重要”的 source/test/history-support 文件来缩小仓库。任何用于解释、验证或复现 v0 行为的项目文件均属于交付物。
6. 审计以 source repository 为 authority。若二进制行为与交付源码不可复现地不一致，则交付失败。

Final Acceptance report 至少包含：

```text
HEAD: <exact 40-hex SHA>
Build command: <exact command>
scp.exe SHA-256: <64-hex>
Source rebuild/auditability: PASS/FAIL
go test ./...: PASS/FAIL
go vet ./...: PASS/FAIL
Architecture constraints: PASS/FAIL
Windows+WSL2 integration: PASS/FAIL
Acceptance A0-A10: PASS/FAIL per case
Role-card schema/golden CLI projections: PASS/FAIL
Scheduler transition table: PASS/FAIL
Frozen walkthrough: PASS/FAIL
Known normative failures: none | list
```

只有全部 required item 为 PASS 且 `Known normative failures: none` 时，最后一行才可以输出：

```text
SCP Harness v0 implementation complete
```

不得以“代码已经基本完成”“主要功能可用”作为交付标准。

## 48. O5 amendment — human discussion and explicit Option release (2026-09-22)

RESOURCE IS NOT AUTHORITY. No Option can start a mutation chain without explicit
host option.release. Database schema_version remains 0. No released/approved/ready
field, Release object, special release Claim, chat/session/thread object or second
scheduler exists. RoundRobin may remain readable for compatibility but never
influences initial mutation dispatch.

`Core.ReleaseOption(id)` / `scp option release ID` requires option.release, existing
OPEN Option, ACTIVE Task, completed initial exploration, positive Option remaining
resource and no Task Pending/cancellation. Fail with NOT_FOUND, INVALID_STATE,
INSUFFICIENT_RESOURCE, BLOCKED or CAPABILITY_DENIED without repairing state. Within
one transaction, acquire the existing nonblocking Task control gate, revalidate,
write Pending[task]={task_id,option_id:id,operation:mutation,target_type:OPTION,
target_id:id}, audit OPTION_RELEASED and commit before unlocking. Never hold the
gate waiting for a SQLite transaction. Release neither reserves the execution slot
nor starts a worker. Only normal scp run consumes the queued step.

One release authorizes the entire mutation/CONTINUE/test/review/reject/rework/
promotion chain, including resource pauses and blocker repair. Promotion, DROP,
invalid mutation, CRASHED, TIMED_OUT, INTERRUPTED and PRECONDITION_CHANGED end the
chain by deleting Pending. Remaining budget does not authorize a retry. Recovery
may resume existing valid Pending chains and reconcile runtime/journals, but must
never infer authorization from balances, including pre-upgrade funded Options.

All creation paths (propose, option_generation, mutation.new_options, refine,
split, explicit merge, merge_synth) create inert candidates. Allocate only moves
resource. Refine preserves immutable text by creating a child; omitted
--transfer-wall-ms means 0, parent balance/status unchanged, child balance 0.
Explicit transfer retains ledger semantics. Refine/split/merge participants that
belong to a Task's Pending chain return BLOCKED. Allocate may add resources to the
active Option; close retains explicit cancellation synchronization.

`option comment ID --text TEXT` requires claim.publish and creates an immutable
informational discussion.comment with subject_type=OPTION, subject_id=ID and exact
payload_json={"text":"non-empty text"}. Core binds issuer and current Task
created_against. It takes no execution slot, moves no budget, creates no Pending,
and works during execution. An already materialized Attempt need not see later
comments. Generic Claims never create a release effect.

`option thread ID` reads only discussion.comment/discussion.reply on that Option,
ordered by created_at,id. Human output is `[time] issuer:` followed by message text;
JSON uses the §36 projection. It creates no persistent thread object.

`option discuss ID --text TEXT` requires option.discuss + claim.publish and the same
Option/Task/exploration/no-Pending conditions as release, but requires positive
Task-root resource, not Option allocation. Under the same Task control gate, one
transaction creates the human comment and Pending(operation=discussion,target=
OPTION ID,option_id=ID), audits OPTION_DISCUSSION_REQUESTED and commits. Failure
rolls back both; it must not leave a misleading human comment. Mutation/test/review/
promotion Pending blocks discuss. Plain comment remains available.

Discussion uses the ordinary bounded worker path and global execution slot, with
Anchor(discussion)=Task root, lease=min(root remaining, profile timeout, card lease).
Profile is readonly with synthetic_git=false. Minimal card context: task.objective,
task.state, option.target, option.lineage.direct, claim.related, ledger.resource.
Capabilities: claim.publish, repository.read(scope=task.repository),
process.execute(scope=lease.sandbox). The authoritative source snapshot is readonly;
normal channel/capability checks still apply. No sandbox.write, release, allocate,
task.*, review.decide or completion capability is assigned to discussion workers.

Core supplies context/discussion-instruction.txt: answer the current human question
with a reviewable conclusion, reasons summary, risks and recommendations; do not
output hidden chain-of-thought or claim to have changed Option/code. Suggestions
stay in the answer; O5 decides whether to refine. claim.related is sorted by
created_at,id. Core is provider-opaque.

O5/operator owns option.release and option.discuss. Ordinary worker cards do not
own option.release. Worker results/new_options are strict and cannot carry release
fields. Claims, review APPROVE, allocation and all creation paths cannot seed a new
chain. The frozen walkthrough and Windows release selection include funded idle,
human discussion, explicit release and idle after promotion, including real Codex
workers under the existing YOLO/OS boundary.
