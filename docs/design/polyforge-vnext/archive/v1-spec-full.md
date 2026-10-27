# v2 架构设计修订（三轮 review 收敛版）

> **ARCHIVED 2026-10-16** (repo review 2026-10-16 wave 4): describes the v0/v1 design and is kept for history only — do not build from it. Current reference: [reference.md](../reference.md); decisions: [v2-converge.md](../v2-converge.md). Live mechanisms this file still held were moved there before archiving; section anchors are unchanged.

> **注意 / Note（2026-10-12）**：本文部分内容已被 [v2-converge.md](../v2-converge.md) 取代；两者冲突时以 v2-converge 为准。Parts of this document are superseded by v2-converge.md; where they conflict, v2-converge wins.

## 〇、Review 轨迹
- 第一轮（初级方案 go-git 内嵌）：判定不可行（嵌套仓库、服务端不成熟、大文件、凭证隔离）
- 第二轮（修订方案 capsule git + .gitignore + 真 git 服务）：判定条件可行，3 个 blocker
- 第三轮（消息驱动 Runtime/Observe 模型）：判定方向正确，10 项必修

## 一、模块结构（pf 二进制内部）

```
internal/polyforge/
├── cli/           13 个动词（纯交互）
├── runtime/       WI 生命周期管理者（手脚）
│   ├── Start      claim + git clone + 物化 + spawn agent
│   ├── Stop       封存 + ship
│   ├── Recover    杀旧进程（五层递进 + cgroup）→ resume/fresh → 告诉 Observe
│   └── Restart    从 state.json + aihub 对账 → 逐 WI Recover
└── observe/       WI 监督者（眼睛 + 大脑）
    ├── per-WI goroutine（监测一切 + 异步 commit）
    └── events file 写入
```

依赖方向：cmd/pf → polyforge → common；aihub 不 import polyforge

## 二、消息协议（Runtime ↔ Observe）

五种消息，每条必带 attempt-id：

| 消息 | 方向 | 语义 |
|---|---|---|
| WIStarted | RT→OB | 新 WI 开始，附 agent handle + attempt-id |
| WIRecovered | RT→OB | WI 恢复（新 agent），附新 handle + 新 attempt-id |
| WIFinished | RT→OB | WI 终态（wrap/fail），停止监测 |
| WINeedsRecovery | OB→RT | agent 出问题，附原因 + 当前 attempt-id |
| WICompleted | OB→RT | agent 说做完了（flush(wrap)），附最终状态 |

**Attempt-id 竞态防护**：
- Observe 发消息必带当前 agent handle 的 attempt-id
- Runtime 丢弃 attempt-id ≠ 当前的消息（防恢复循环）
- Observe 发出 WINeedsRecovery 后进入 RECOVERING 态，抑制旧 handle 的重复死亡信号
- monitor goroutine 等待恢复时 select { WIRecovered, stopSignal }（防死锁）

## 三、Runtime 内部状态机（per-WI）

```
RUNNING → SEALING → DONE
RUNNING → RECOVERING → RUNNING（恢复成功）
RUNNING → RECOVERING → DONE（恢复失败 2 次 → bounded fail）
SEALING 丢弃同 attempt-id 的 WINeedsRecovery（如果 push 已落地）
```

## 四、Observe 监测协程（per-WI）

**核心约束：select case 里永不阻塞。**

长操作（push、等答案、probe 等待）启动子协程，通过 channel 回到 select。

## 五、控制协议（daemon ↔ agent，4 方法 + reason 判别）

| 方法 | 方向 | 阻塞？ | reason 判别 |
|---|---|---|---|
| flush(reason) | agent→daemon | 是（60s 超时） | needs_input / wrap / shutdown |
| answer | daemon→agent | — | — |
| stop_notice | daemon→agent | — | — |
| probe | daemon→agent | 2m 超时 | — |

**flush 超时语义**：
- 60s 超时 → 类型化错误
- needs_input 失败 → 降级：写 events 文件（不同步 push）
- wrap 失败 → 不封存，重试 3 次后转 blocked
- shutdown 失败 → 不等待，退出（数据在本地 commit 中）

## 六、Git 修订链

### Capsule git 仓库
- git-dir 外置：/var/lib/polyforge/git/wi_xxx.git（agent 够不到 hooks/config/index）
- .gitignore：repos/ + artifacts/release-*/ + session-data/
- **per-WI git mutex**：Runtime 和 Observe 串行化
- **stale index.lock 清理**：重启时检查持锁 PID 是否已死
- **commit-dirty-on-recover**：恢复前先把脏的 capsule 状态 commit + push
- **无 trailer 提交自愈（repo review 2026-10-16 V-2）**：capsule 根的 `.git` 指针使 step 在根目录的 `git commit` 落到链分支且无 seq trailer；CommitPush 在一切之前检查 HEAD，若不是最新的带 trailer 提交，则 `git reset --soft` 回该提交（内容保留在暂存区，随下一个带正确 seq 的提交发布），并发 `untrailered_commits_folded` 事件。**不自愈**：step 对已推送的带 trailer 提交做 `git commit --amend`（或其他改写）——推送是 non-fast-forward，被 hook 拒绝，需人工处理。

### aihub git 服务
- git http-backend 代理过 aihub 现有 HTTP 端口
- pre-receive hooks：FF-only、拒绝 force-push/ref 删除、拒绝 gitlink(160000)、seq trailer 单调递增、blob 大小限制（capsule 链 16 MiB `capsule.MaxBlobBytes`；task-repo carrier ref `refs/pf-task/*` 100 MiB `capsule.MaxTaskBlobBytes`，polyforge#158）
- **Generation fencing 两层**：
  - 主层：attempt-scoped push credential（takeover 吊销旧凭证 → 旧 daemon push 被 auth 拒绝）
  - 备层：pre-receive hook 校验 push-option 中的 attempt-id
- 跨仓库原子性：capsule 链先推、task-repo carrier ref（`refs/pf-task/<name>`）后推，后者失败不回滚 capsule 链（D8 裁定，polyforge#150：以代码为准），除日志外，链推送成功时还发 `carrier_push_failed` 事件、由 watcher 作为人工决策提醒（polyforge#158，见 `05-sync-revision-recovery.md#§7.5`）；恢复时容忍 capsule 引用未落地 task-repo SHA
- 定期 gc/repack

## 七、双轨恢复

判据：转录在不在（不是同机/跨机）

```
转录存在 → resume（pi --session <path>，秒级）
  连续 resume 失败 2 次 → 切 fresh
转录不存在 → fresh derivation（git clone，分钟级）
  新 agent 可读 capsule 里的旧转录理解上代思路
```

## 八、进程清理（六层递进）

```
Layer 0 (Linux): per-WI cgroup v2 → cgroup.kill 原子杀全部后代
Layer 1: PPID 快照（杀前 walk /proc，100% 精准）
Layer 2: kill(-pgid, SIGTERM/SIGKILL)
Layer 3: 快照补杀（用 (pid, starttime) 防误杀）
Layer 4: PF_WI_ID 环境标记扫描（/proc/*/environ）
Layer 5: cwd 扫描 → 仅告警不杀（防误杀操作员）
```

已知盲区：Docker 容器 → 用 --label pf_wi=xxx 标记容器

## 九、异步 commit + 背压

- Agent 零阻塞（写 execution.json → 继续）
- Observe 检测变化 → 异步 commit + push
- **背压策略**：N 步未推送（默认 5）或 T 分钟失败（默认 30min）→ 降级事件
- 恢复时 commit-dirty-on-recover 兜底

## 十、转录策略

**转录不进 git 仓库**（含凭证 → secret 扫描拒绝推送 → WI 无法 wrap）。

替代：旁路上传（同 release 二进制）：
- 每步拷贝到 capsule session-data/（.gitignore 排除）
- 单独 HTTP 上传到 aihub sessions/wi_xxx/
- 命名 session-<attempt-id>.jsonl（保留历史不覆写）
- 跨机恢复时从 aihub 下载

## 十一、重启对账

Runtime 重启时不只读 state.json——同时从 aihub 拉 WI 终态：
- aihub 说 wrapped → 不复活
- aihub 说 running → 杀旧进程 → Recover
- aihub 说 blocked → 不动

## 十二、代码量估算

```
internal/aihub/       ~2500 行
internal/polyforge/   ~2900 行
internal/common/      ~500 行
合计                  ~5900 行（当前 ~17000 行，减 65%）
```

## 十三、实现拆票
1. aihub git proxy + pre-receive hooks + reconciler
2. Runtime 模块（Start/Stop/Recover/Restart + 进程清理）
3. Observe 模块（monitor goroutine + events + answer relay）
4. 控制协议改造（flush(reason) + probe + answer + stop_notice）
5. pf-execute skill 修订
6. 双轨恢复实现
7. 模块重排（internal/ 9 包 → 3 目录）
8. 回归重写 + 新场景 e2e
9. 迁移方案（SQLite 修订链 → git 修订链）
