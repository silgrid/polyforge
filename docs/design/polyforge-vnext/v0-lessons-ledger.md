# v0 Lessons Ledger — every real-machine failure v0 ever hit
# (source: pilot history, HANDOFF, ticket records; the authoritative
# registry the v1 implementation should have been audited against)

> **注意（2026-10-12）**：`docs/pilot/` 已删除；本文引用的 pilot 路径请到 git 历史查看（tag `v2.1.0`）。
>
> **Superseded marks (repo review 2026-10-16 wave 4)**: entries whose *mechanism* no longer exists carry a
> `〔SUPERSEDED …〕` line naming what replaced it; the lesson itself still applies. Open entries fixed
> later carry a `〔FIXED …〕` line naming the change, and one still open in a changed form an `〔OPEN …〕`
> line giving its current form and ticket (marked 2026-10-18). Paths such as
> `03-execution-capsule` now live under [archive/](archive/); the current system is [reference.md](reference.md).

## Protocol/harness class
P1  Real pi rejects invented RPC envelopes (v0 first pilot ticket:
    "Unknown command: undefined"). v1 spawn envelope must be the v0
    live-tested prompt-command/record-stream shape.
P2  Probe argv must be faithful: --no-tools was NOT a real pi flag (v0
    probe found it; removed). Never invent harness flags.
P3  PI_SESSION_DIR does NOT control pi's transcript location. Real
    transcript: ~/.pi/agent/sessions/<dashify(cwd)>--/<ts>-<uuid>.jsonl.
    pi CAN resume from any .jsonl path via --session.
P4  cc protocol: UUID sessions, stream-json bidirectional stdin,
    --resume <uuid>, zero-turn crash fallback (same UUID fresh).
P5  WI Agent inherits the operator's GLOBAL pi config unless isolated:
    skills/context-files/prompt-templates (3 flags), env (PF_*/
    POLYFORGE_* strip + secret value strip), and ambient MCP/extension
    registrations leak other systems' toolfaces (the operator's
    mcp-adapter.json). --no-extensions must STAY absent (subagent tool
    is extension-provided) — residual exposure closed by instruction.
    〔SUPERSEDED (2026-10-16): the WI Agent is deleted; step agents run with a dedicated step-agent dir (no settings.json, only extensions/subagent) and PF_* stripped — reference.md §7.6. The lesson stands.〕
P6  Model selection is per-step bundle business (model_candidates with
    ordinal advancement on INFRA failure only — business FAIL never
    advances). The agent must not silently run on the operator's
    global default model/settings (settings pollution: e.g. max_tokens
    values the operator set for another provider break the inherited
    provider with 400s).

## Provider/infra class
I1  Provider 400/503/connection-hangs are normal weather. Detection:
    message.stopReason=="error" + errorMessage records.
I2  Candidate advancement must be bounded and the ordinal persisted
    in execution state (crash-safe).
I3  wall_minutes parsing: float vs integer mismatch made budget gates
    misfire (v0 #46).
I4  Wait, hang and stream windows need injectable bounds or tests
    take hours (v0 shipped parameterized windows).

## Sync/ship class
S1  Ship once pushed the BASE sha instead of the sealed HEAD (v0 #46
    era). Ship must verify remote branch == sealed head.
S2  Sync roots excluded repos/ and release trees from the capsule
    revision chain (v1: .gitignore equivalents).
S3  Stale-generation quarantine: pushes with superseded credentials
    must be fenced server-side (attempt-scoped credentials; takeover
    revokes).
    〔SUPERSEDED (2026-10-16): generations/attempt credentials and takeover are deleted; pushes are fenced by the live session secret (GIT-401) and recovery is force-end-session + a normal claim — reference.md §3.4. The lesson stands.〕
S4  Publish/flush semantics: wrap/shutdown flushes are blocking with
    typed timeout (60s); shutdown with dead agents must not wedge
    (v0 #32: 20min wedge -> 5min bounded shutdown budget).
    〔SUPERSEDED (2026-10-16): the flush lane is deleted; pushes are event-triggered with a 60s bound and the shutdown has a 5-minute budget — reference.md §6.2, §8. The lesson stands.〕

## Daemon/lifecycle class
D1  Dead agent + detached bash descendants escape the process group —
    six-layer cleanup with (pid,starttime) guards; PF_WI_ID env marker.
D2  Agent death mid-answer-relay: answers must not be lost — idempotent
    3-state ledger, failure never consumes the answer id; new attempt
    must see the pending question (re-injection).
    〔SUPERSEDED (2026-10-16): the answer ledger is deleted; answers are archived in execution.json by the driver and a re-dispatch re-asks the pending question — reference.md §7.3. The lesson stands.〕
D3  Restart must reconcile with aihub before reviving (wrapped -> don't
    revive; running -> kill+recover; blocked -> leave).
D4  Claim idempotency: re-sending the same key returns the same attempt
    (network retries); takeover requeue idempotency.
    〔SUPERSEDED (2026-10-16): claim idempotency is keyed on the session secret: the holder's repeat answers the same claim, a superseded or ended secret is refused CORE-403 STALE_SESSION; takeover is deleted — reference.md §3.2. The lesson stands.〕
D5  Stale claim detection: a crash between claim and spawn must not
    orphan a generation forever (v0 known gap #Start-crash).
    〔SUPERSEDED (2026-10-16): the pre-claim claiming journal (claiming.json) is the fix — reference.md §3.5. The lesson stands.〕
D6  needs_input had NO proactive owner notification in v0 (watcher
    scripts filled the gap). v1 answer: check at turn start / events
    file — the events file exists; owner-facing surface?
D7  Shutdown sequence: stop_notice -> flush(shutdown) -> seal; never
    let observe re-derive an agent after seal.
    〔SUPERSEDED (2026-10-16): the shutdown is latch → cancel → kill agent trees → refs/pf-wip snapshot (parallel, 30s) → persist, no flush, no session end — reference.md §6.2. The lesson stands.〕

## Data/capsule class
C1' (v0 lesson, superseded by v1 C1 ruling) transcripts are valuable:
    in v1 they must actually REACH the capsule git (sync mechanism was
    the missing piece — see P3).
C2  Work-item context delivery: v0 delivered goal/content via daemon
    proxy_call (read_workflow_ref). When a contact surface is deleted,
    enumerate the business it carried before deleting it.
C3' execution.json is the only progress authority; Version must bump
    on every change the daemon should observe.
C4  Digest integrity: bundle digest pinned at claim must be verifiable
    (v0 verified server-side; v1 wires it at materialize).
    〔SUPERSEDED (2026-10-16): the bundle digest chain is deleted (polyforge#149 B9); bundles are referenced by name only — 01-workflow-skills.md §4.5. The lesson stands.〕

## Process class
X1  Smoke commands given to agents need timeout + explicit stdin close
    (#31 40min wedge).
X2  pkill/kill argv self-match traps: always script files, never
    inline patterns.
X3  One-shot agents must not start background tasks; tests run in
    foreground.
X4  The memory ledger exists precisely to prevent re-hits — every
    implementation/review lane MUST recall it first (v1 lanes did not;
    that is why F1/F3/F4 happened).

## v1 pilot findings (F1–F7, F11–F22) — the lessons the ledger itself predicted

X4 said every implementation/review lane MUST recall this ledger first; the
v1 pilot lanes did not, and every one of these findings is a re-hit of an
entry above. Recorded here so no future lane re-derives them. F8–F10 are
spent in git history and reserved for a future backfill — the numbering gap
is deliberate, not an error.

F1  Goal never reached the agent. v1 deleted v0's daemon proxy_call
    (read_workflow_ref) without enumerating the business it carried (C2):
    with no agent→server read path at all, the WI agent could not even know
    its own goal. Fixed: the claim answer's task definition is materialized
    in full as runtime-state/work-item.json (the ONE authoritative statement
    of the ask inside the capsule) and recovery repairs a missing copy.
    Fixed in main (v1-fix-pilot1, merged as 1059da5 / pilot commit 39dfd1c).

F2  Slug-built git paths. A submitted "<project>#<seq>" spelling leaked into
    the push remote URL (a 404 — "#" is the URL fragment separator) and the
    capsule directory name. Fixed: the claim answer's canonical work-item id
    is the authority; every path (capsule dir, external git-dir, push
    remote, session id, spawn journal) is keyed by it, the submitted
    spelling is registered as an alias only, and Restart migrates
    pre-canonical residue in place. Fixed in main (v1-fix-pilot1, 1059da5).

F3  Transcripts never reached the capsule. The v0-ported PI_SESSION_DIR env
    is dead for transcript placement (P3): a fresh derivation writes to the
    harness's own session store, nothing landed in capsule git (C1'). Fixed
    daemon-side: locate the live transcript after spawn, copy it into the
    capsule at every commit boundary (before the commit, so it rides the
    chain), and resume from the capsule copy with the live file as fallback.
    Fixed in main (v1-fix-pilot1, 1059da5).

F4  The candidate chain was a decoration and settings pollution was open
    (P6 + I2). The bundle's model_candidates were never consumed: the spawn
    pinned no model (the agent silently ran on the operator's global
    default), the ordinal was neither advanced nor persisted, business
    FAIL/rewind semantics were not wired (INV-1g), and the observe face's
    StepView.Model rendered permanently empty. Fixed: the minimal
    frontmatter read surface (defaults + per-step model_candidates) is back
    in common/workflow; materialize extracts the defaults chain onto the
    job; every derivation spawn is pinned to the candidate at the persisted
    ordinal (argv — measured: capsule-local project settings are
    trust-gated OFF in rpc mode, see spawn.go buildCmd); ONLY a
    provider-class recovery advances the ordinal (bounded, wraps —
    exhaustion is not terminal) and the advance is crash-safe in
    state.json; the executor records the effective model into
    steps.<id>.model and the observe face renders it.
    Fixed in this lane (v1-lessons-fix, base 1059da5).

F5  Fatal provider error 处置与 idle 看门狗（I1 re-hit）：fatal 级 provider
    错误此前按普通中断处理（不自恢复、不能及时判死），长静默步无看门狗。
    Fixed: fatal provider error 即刻恢复 + idle watchdog（10 分钟窗口 +
    probe）。已修，main d84115b（分支 v1-f5-watchdog，branch commit
    2a0ab4a）。

F6  Goal 携带的 recall 指令不可执行（X4 re-hit）：agent 无 agent→server
    memory 读路径，无法先读台账再动手。Fixed: materialize 时静态记忆快照 +
    UDS 窄通道 recall/remember + CLI memory 动词（`pf recall` /
    `pf remember`）。已修，main c740fb0（分支 v1-f6f7，commit e0db578）。

F7  步定义物化错位与完成标记缺失（03-execution-capsule §4.5 re-hit）：
    物化把步定义复制到 capsule `agents/` 而非 Pi 发现目录 `.pi/agents/`，
    且未写完成标记。Fixed: `.pi/agents` 物化目标目录 + 完成标记
    `sync/materialized.json`（RelMaterializeMarker）+ trust gate
    （snapshot 仅在 marker 完整且匹配 pinned bundle 身份时可信）。已修，
    main c740fb0（分支 v1-f6f7，commit e0db578）。

F11 e2e daemon 拆除泄漏 fakepi 孤儿（D1/X3 re-hit）：e2e 各拆除路径
    （graceful stop、crash、SIGKILL fallback 等）拆除 daemon 后，带场景
    `PF_WI_ID` 标记的 fakepi 孤儿进程仍可存活。修复：拆除后扫描 /proc 中带
    该场景 PF_WI_ID 标记的存活进程，SIGKILL 全部幸存者，并以有界轮询断言
    零残留（作为场景 FIRST cleanup 注册，LIFO 保证在所有拆除 cleanup 之后
    运行）。状态注记：已修（main d586396，in HEAD feaf127；测试改动
    test/v1_harness_test.go +70、s12/s13 各 +3）。

F12 停机 push 竞态（S1/#46 re-hit）：daemon 停机时 flush(shutdown) push
    （observe/monitor.go flushShutdown，~line 1333）与异步 archival push
    （schedulePush，~line 901）/ 进程退出竞速，远端可落后于本地 sealed
    head——v0 #46「push 必须终于 sealed HEAD，绝非 stale/base sha」
    教训类的 v1 再命中。状态注记：已补偿（由 #46 修复的校验面兜底：
    ship.go ls-remote 校验 remote==sealed commit、绝不 force-push 已移动
    的分支（~lines 160–200）；重启和解补完 seal——测试 fa9ed17「s17
    SIGKILL inside the SEALING window」）。
    〔SUPERSEDED (2026-10-16): flush(shutdown) is deleted with the flush lane; see S4. The lesson stands.〕

F13 maxTokens 双事故（P6 re-hit ×2）：操作员全局模型配置两起分别设
    maxTokens 超过/等于模型 contextWindow（163840 > 131072、
    131072 = 131072），继承 provider 均以 400 崩——settings 污染两连击。
    纪律：操作员模型配置须 maxTokens << contextWindow。状态注记：已补偿
    （spawn.go 刻意不设 max_tokens——被 pin 模型自身默认即保守选择，自造
    上限恰是 v0 settings 污染失败形状的另一面（注释块 ~line 234）；F4 的
    argv 模型 pin 移除 defaultModel 继承向量；残余 per-model 操作员覆盖
    为文档化故意接受）。

F14 boot.sh 全栈重启误伤：2026-09-30 操作员想重启单个 daemon，跑了
    `/root/polyforge-pilot-v1/boot.sh`（全栈幂等重启器：kill 旧 aihub +
    双 daemon 后三者全部重拉），整个 v1 pilot 栈被一并停掉。
    状态注记：已补偿（单点重启脚本已建：`boot-b-only.sh`，2026-09-30
    11:46 创建——只 kill daemon-b、轮询 ≤120s 等 `b/daemon.sock`
    释放、setsid 单独重启 daemon-b；仓库内已录于
    `docs/pilot/ops-scripts.md` 运维注意事项，lines 75–77）。

编号对账（polyforge#51 追加批）：本 WI goal 写作「台账补 F13-F17」时
F13/F14 尚不存在，其后 #47（commit `c1c55be`）已追加 F13/F14，故本批
自 F15（下一空号）起连续编号；F8–F10 仍按上台账原注刻意保留、不回填。
phase-0 观测窗真机发现实际共 6 条（F15–F20），多于 goal 暗示的 5 个
槽位——按证据如实登记，不为凑数增删。

F15 watchdog 进度语义盲区「UI noise is not progress」（F5 引入的看门狗
    自身的新失败形状，非 v0 条目再命中）：真机两连击——subagent 派发在
    双盲看门狗下楔死 2.7 小时（hang 判据要求静默，被 streaming
    extension_ui 噪声持续刷新 lastRecordAt 挡住；idle 判据要求无开放
    turn，被真实开放的 turn 挡住——两判据互盲，谁都不开火）。修复：
    记录分流 progress（message_*/toolResult/lifecycle/response）vs noise
    （extension_ui_request/未分类/不可解析），Silent() 与 idle 判据锚定
    LastProgressAt（shape A/B/C 三形 + s21 e2e 对真 daemon 钉死；另加
    no_progress owner 可见事件、ops status 的 LAST-PROGRESS 列）。
    状态注记：已修（main `ef67aa7`，分支 v1-f15 merge `d485c72`；修复
    落地前 #46 期间由手动 shim `/root/polyforge-pilot-v1/f15-shim.sh`
    补位——capsule transcript 静默 >15min 且 WI 仍在跑即杀进程树交恢复
    接管，WI 终态自退出）。

F16 flush 栅栏收养窗洞（S3 同型：被取代的世代必须被栅栏挡住）：monitor
    滞后的 current-id 若仍等于 flush 方 id，在 hub 最新已知已指向后继时
    其实早已 stale——只有僵尸会用前代的名字说话，旧栅栏却放行这种
    「current-id 命中」的 zombie flush。由 F15 修复引入的时间扰动暴露
    （s10 红，wait 修复后确定性复现）。状态注记：已修（main `a91ef69`：
    flush fence 增加 hub 最新已知的收养窗 cross-check；s10 改等 monitor
    收养事件（wi_agent_rederived）而非 spawn journal；
    flush_recovery_window HTTP 测试钉死收紧语义；同 commit 另将 s19
    ShipPR 的 gh 建仓限流按可用性门处理——skip 不 fail）。
    〔SUPERSEDED (2026-10-16): the flush fence is deleted with the flush lane; dispatch fencing is spawn_n in the driver — v2-converge.md §2.2. The lesson stands.〕

F17 M4 证据链断裂（C3' 同型：执行权威文件里该有的证据从未落进）：39 个
    已执行 verification step 中 17 个无结构化命令证据（12 个旧 schema
    execution.json 无 commands 字段 + 5 个 commands 无可读条目），
    M4 = 22/39 = 56.41%，PILOT-V4 否决触发——触发的主体是证据面缺口
    而非谎报证实，且旧 schema 票无法追溯补分。状态注记：已修（V4/V8
    修复 lane，台账 F21：verification 证据契约三层硬强制 + wrap 侧
    fail-closed；分支 v1-v4v8，合入后回填 commit）。

F18 持续类拒绝下的 push 风暴（S4 有界性裁定在发布重试面的缺失）：
    wi_VCyBYf7uYeb2（polyforge#48）对同一 capsule 以 ~10s 固定节奏连推
    185 次全部 push_failed（前段 aihub gitlink mode-160000 拒绝、后段
    commit c82791fd… 拒绝，另 2 次 wrap_blocked），无退避、无熔断、无
    上报升级，风暴持续约 1 小时；该批样本直接把 M8 推到 188/647 =
    29.06% > 1%，PILOT-V8 否决触发（M7 仅计 1 段 ≤ 3，未触发）。
    状态注记：已修（V4/V8 修复 lane，台账 F22：毒链本地预检
    GIT-412 + push_poisoned_local 分类 + 同错误重试上限；分支
    v1-v4v8，合入后回填 commit）。

F19 M6 观测字段从未随事件面发出：整个窗口 events.log 共 33 条
    memory_remember 事件、携带 write_ms 字段 0 条（旧 schema 事件面未含
    该观测字段；迁移 CLI 直写与服务端 reconcile 回写不产生事件——已
    登记的样本集边界），PILOT-V6 本窗口无样本、不参与判定：检索实时性
    面（2026-10-16 前版本的 06-memory-search §4.5 p99 ≤ 5s，见 git 历史）全程不可观测。状态注记：
    未修——已登记为 phase-1 输入（`docs/pilot/v1-phase-0-report.md`
    §5.2「M6 样本集边界」：需新事件 kind 或样本面补齐，未获授权前不动）。
    〔SUPERSEDED (2026-10-18): since v1 (b1839e23) memories are written through aihub only — Store.Create inserts the row and its FTS row in one transaction (06-memory-search.md §2), so a memory is searchable the moment `pf remember` returns; no write-to-search latency is left to observe, and the p99 ≤ 5s goal (060c9f9a), the M6 gate and docs/pilot are deleted. The lesson stands.〕

F20 M3 心跳面未接线：整个窗口 aihub.db machines 表 0 行——机器心跳面
    从未被写入，M3 只剩 step 时长面（in_progress > 24h）可判，机器失联
    面（last_heartbeat_at 超存活阈值）无数据，`--m3-heartbeat-max-age`
    无真实数据可配。状态注记：未修——已登记为 phase-1 输入
    （`docs/pilot/v1-phase-0-report.md` §5.2「M3 心跳面未接线」）。
    〔FIXED (2026-10-02, 65b33708 "daemon+aihub: wire the M3 heartbeat face"): the daemon heartbeats aihub on its own goroutine (internal/polyforge/daemon/heartbeat.go) and `pf machines list` reads machines.last_heartbeat_at — e2e test/v1_s24_machines_cli_test.go.〕

F21 V4 证据契约机械强制（PILOT-V4 否决门槛的修复面；对应发现 F17）：
    三层同一谓词硬强制 verification 步的结构化命令证据——驱动契约
    （`skills/pf-execute/SKILL.md` §4：verification 步标 `done` 的同一次
    写入必须已携带至少一条可读 `{argv, exit_code, duration_ms}`，无证据
    不许写 done；中译镜像 `references/agent-contract.md` §十一同步；四个
    参考包的 `agents/verification.md` 补「Evidence obligation (hard
    gate)」硬约束）、daemon 侧 fail-closed（observe 的 flush(wrap) 子协程
    校验 execution.json 全部已执行 verification 步，缺证据的 seal 答
    `DAEMON-400 VERIFICATION_EVIDENCE_MISSING`（引票 + step 名），非终局、
    补证据后重 flush 过；事件面 `wrap_evidence_refused` 留痕），谓词与
    M4 读数器（`tools/gate-metrics.go` 的 `commandEvidence`）逐位一致
    （step id `verification`、已执行 = 非 pending/非空、argv 非空 +
    exit_code/duration_ms 可读）——门与指标判定同一张脸，未来无证据
    verification 不可能再通过 wrap。回归：observe 单测 + e2e
    `v1_s22_evidence_gitlink_test.go` S22a。状态注记：已修
    （分支 v1-v4v8；合入后回填 commit）。
    〔SUPERSEDED (2026-10-16): the evidence gate lives in the driver envelope check (requires_command_evidence), not in observe flush(wrap); skills/pf-execute and tools/gate-metrics are deleted — reference.md §7.3. The lesson stands.〕

F22 毒链本地预检 + 同错误重试上限（PILOT-V8 否决门槛的修复面；对应
    发现 F18）：gitops 的 CommitPush 在 `git add -A` 后、commit 前扫描
    `git ls-files --stage`，出现 mode 160000 条目即类型化错误
    `GIT-412 LOCAL_GITLINK_DETECTED`（不产生 commit——毒链根本进不了
    本地历史，#48 事故的「毒 commit 已产生只能反复推」形状从源头消
    除；错误信息给出污染路径与清除指引〔2026-10-16 更正，repo review B-25/V-13：清除指引**绝不**让人删任务仓库的
    .git——`repos/` 或 `artifacts/release-*` 下出现 gitlink 只说明 capsule `.gitignore` 丢了对应行，指引是恢复
    `.gitignore` 后重试，并明言不要删该 `.git`（那会毁掉任务仓库历史，含未推送工作）；其他位置的嵌套仓库是
    agent 残留，指引是把它移出 capsule 或写进 capsule `.gitignore`，仅当其历史可丢弃时才删其 `.git`，
    再 `git rm --cached` 并以 `git ls-files --stage | grep ^160000` 为空验证〕；
    嵌套 .git 无 commit 时 add 直接失败的同型错误同样归类）；observe
    的 push 路径把它归类为新事件 `push_poisoned_local`（与 push_failed
    分开——本地预检拒绝的是「从未合法存在的 commit」，不是「已发生的
    边界同步请求失败」，不进 M8 分子分母；钩子端拒收若仍发生照旧计
    push_failed），同时自动重试加上界：连续 5 次同错误即停本轮重试、落
    `push_backpressure(reason=same_error)` 降级事件，等下一个边界
    （execution.json Version bump）或一次成功 push 再续——防 185 次风
    暴再现。回归：gitops 单测（本地嵌套 .git → GIT-412 不产生 commit）
    + e2e `v1_s22_evidence_gitlink_test.go` S22b（5 次拒绝后停，清理后
    链恢复、裸仓历史无 gitlink）。状态注记：已修（分支 v1-v4v8；
    合入后回填 commit）。

编号对账（V4/V8 修复 lane 追加批）：本批 goal 原写「F18/F19」时 F18
    （push 风暴）与 F19（M6 观测字段）已由 #51 追加批占用（F15–F20 连
    续编号），故本批自 F21（下一空号）起登记，并把两条修复面分别对应
    到其真正修复的发现：F21 修 F17（M4 证据链断裂）、F22 修 F18
    （push 风暴）；F17/F18 的状态注记同步指向本批。

## 追认记录（2026-10-01，owner 逐条确认）

Phase-0 观测窗期间由操作员 agent 代理的 14 项设计/运营级裁定，
已向 owner 逐条追认：14/14 过，零推翻。
涉及：flush 围栏收严（F16）、git 面读不开后门、转录 commit 前同步、
答案先落盘再投递、停机双进程先 reconcile 再开 UDS、版本号抢号、
release 授权批量（alpha.4-30，只新建不覆盖不升级）、tag 不推 origin、
e2e 缺口全开真票、M6/M8 最小仪器化、V4 wrap 证据硬门、V8 毒链本地
预检+防御不计失败、needs_input 代理答题（自本条起改为 owner 亲自答）、
#52 takeover 提前。

## v1.5 附录（2026-10-01，session-model 实施轨迹）

v1.5 session-model 实施期间的设计裁定、评审轨迹与残余未修项，按
本台账 house style 追记（append-only；不触动上台账任何条目）。

1. 总述：v1.5 session-ID ownership model（session_id CAS claim、
   fencing-on-use、spawn_n、quarantine）取代 generation/attempt/
   instance lineage，作为协调层替换合入 main @ b5ab060；lineage 全删
   ——aihub（b1dba93）、wire types（03dc291）、runtime（5e440db）、
   observe re-key onto spawn_n（41da6e7）、daemon/cli render spawn_n
   + session_fingerprint（da250ef）。设计权威记录指向
   `v1.5-session-model.md`（含 industry-research appendix：epoch+
   fencing 家族、invalidation-on-claim vs fencing-on-use、ZooKeeper
   GUID ambiguous-claim 教训）与 `v1.5-api-contract.md` §1.5 AMEND。
   状态注记：已落地。

2. B1/B2/B3 裁定与解决：三条 fable BLOCKER 强制追加项，在实施评审
   （commit 3158d25，verdict APPROVE-WITH-FINDINGS）全部 RESOLVED
   with evidence——B1 pre-claim durable journal（崩溃安全，D5
   #Start-crash 修复兼 CAS retry-idempotency 锚，正面回答 ZooKeeper
   GUID ambiguous-claim 教训）；B2 flush fence = (session_id,
   spawn_n) 双键（取代收养窗栅栏，F16 修复的接棒者）；B3
   identity/credential split（PF_SESSION_HANDLE 仅展示指纹 vs 受
   栅栏保护的 push 凭据，GIT_ASKPASS 单次投递）。状态注记：已落地。

3. M1–M5 裁定落地（逐条短条目）：
   - M1 空会话既终态又可领 → CAS 改 (status, session) 联合判定，
     claim gates 恢复。状态注记：已落地。
   - M2 bounded fail：session 清空 + status=blocked，restart 不动，
     blocked 排除出 CAS 可领集合直至 unblock。状态注记：已落地。
   - M3 paused-with-session：SessionEnd 第 4 态 `paused`、
     StatePaused 加入 runtime 状态机、clean_pause 标记、operator
     pause 保留 job record（secret 本地存活，pf resume 兑现续跑
     承诺）。状态注记：已落地。
   - M4 周期性会话重验：monitor tick 上重验（~30 秒；常量见
     `internal/polyforge/observe/session.go`：defaultRevalidateEvery
     30s / verifyErrorBackoff 30s / verifyCallBound 70s），由
     3158d25 补上 Lane D 缺口接线；`--revalidate-every` 旗标由姊妹
     胶囊 wi_brFjFISWrAFC 落地（fa2e091、4045576、0b6ac8a）。
     状态注记：已落地。
   - M5 同机双 daemon TOCTOU → daemon data dir 上 flock。
     状态注记：已落地。

   追记 §1.5 AMEND 批准的实施后 delta：paused 第 4 态、StatePaused
   retain、POST /work-items/{id}/unblock + `pf unblock`
   （admin-or-maintainer，M2 的出口门）。

4. 评审轨迹（三轮回合）：先 fable 设计评审——REQUEST-CHANGES，强制
   B1/B2/B3 + M1–M5；实施评审 @ 3158d25——APPROVE-WITH-FINDINGS
   （B1/B2/B3 全 RESOLVED；3 新 MAJOR：OBSuperseded 丢失、pause
   摧毁 record、无 unblock 门；另 2 MINOR）；修复 lane e2137f5 +
   合入 856ff5c——CONFIRM-APPROVE。

5. NEW-1：陈旧 OBSuperseded 对后继 job 的会话指纹栅栏修复——commit
   3114915「v1.5 NEW-1: OBSuperseded fenced on the session
   fingerprint」（+ gofmt b5ab060）。状态注记：已修。

6. NEW-2：姊妹胶囊 wi_y50ZfykBewso（已 wrap）——commits 7786e99
   「runtime: bounded release lane for observe releases (NEW-2)」+
   f1fb1ee「daemon: close the runtime release lane at shutdown」，
   随 v0.3.2 发布（tag 在 f1fb1ee）。界定 terminal RTFinished
   observe release 的重发上限——observe 通道满时推迟而非摧毁被
   取代/暂停的 release。状态注记：已修——但留一条非阻塞残余（未修
   候选跟进）：observe 侧 RTFinished handler
   （internal/polyforge/observe/observe.go ~296-307）不感知
   spawnN，复合竞态窗内延后的 RTFinished(paused) 可能在后继
   RTStarted 之后送达并无条件删 latestSpawn、Forget 答案台账、
   停掉新 monitor；另注：长寿非 daemon Runtime 不 Close 则 lane
   goroutine 泊住。
   〔SUPERSEDED (2026-10-18): the residual is moot — latestSpawn and the answer ledger are deleted (b1f336e8; 34fdf867), the release lane became unbounded FIFO mailboxes (f0f8cd36, v2.2.0), and since the per-work-item actor (bd17b6af, #190) all of a work item's RT messages are pushed from its one actor goroutine into a single-consumer FIFO, so a stale RTFinished cannot arrive after its successor's RTStarted. The observe handler is still spawn-agnostic and relies on that ordering. The lesson stands.〕

7. NEW-3：姊妹胶囊 wi_YONafuu9hiy9（已 wrap）——commits 01ddc26
   「runtime: replay a pending terminal close of a retained Paused
   record at restart (NEW-3)」+ c297133「docs: mark NEW-3 resolved
   in the v1.5 session-model record」，随 v0.3.3 发布。
   状态注记：已修——但留一条非阻塞 SIBLING 窗（未修候选跟进）：
   restartResume 的 StatePaused 分支仍忽略 pending terminal close
   （需仿 retain 分支的修复、咨询 isTerminalStopReason）；FAILED
   pending-close 回放无独立失败事件（仅 stderr 呈现）。
   〔OPEN (2026-10-18): restartResume is deleted (bd17b6af, #190), but reconcileRecord still resumes a Paused record carrying a terminal stop_reason when the hub answers running, or paused without clean_pause — only paused + clean_pause replays the close — and #190 dropped 735a9ad9's resume guard; a failed fromPaused SessionEnd is not retried during the run, and a failed replay still emits no failure event. Tracked in polyforge#197.〕
   〔FIXED (2026-10-18, PR #205 "a pending terminal close is replayed, never resumed"): a pending close replays whenever the hub shows the session live, whatever clean_pause says; pf resume and a repeat close are refused while it is pending; a failed close of a retained Paused record is retried during the run; a failed replay emits pending_close_replay_failed.〕

8. NEW-9：跨机 spawn_n ordinal 碰撞，保持 open，cosmetic/audit-only。
   状态注记：未修（故意接受，仅审计面）。

9. 收尾注记：运维演练 D1–D5 全 PASS（commit 8b2a249，报告在
   docs/design/polyforge-vnext/，runbook
   docs/pilot/v1.5-drills-runbook.md af3e4a5）；本台账仍是唯一失败
   记忆权威——正是本附录存在的理由（X4 纪律的延续）。
