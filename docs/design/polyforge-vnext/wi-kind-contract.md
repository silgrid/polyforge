# WI kind 契约（polyforge#140 拆分 a：aihub 端） <!-- wi-kind-contract -->

> 状态：拆分 a 交付物（2026-10-13）。供拆分 b（pf 客户端：CLI / skills / daemon submit 预检）对接的**接口契约**；裁决背景见 `v2-converge.md#§12`。
> 本票无 schema 变更、无迁移、无新表新列；**部署**与**回滚**见 §4。
> 通用约束：全部新增面**只做加法**——旧字段不改名、不删减，旧客户端对未知 JSON 键天然忽略。

## 1. `bundle_name` 的请求与响应字段 <!-- §1 -->

- **create**（`POST /api/v1/work-items`）：请求体可选 `bundle_name`（非空字符串）。非空 = 执行票；NULL / 缺省 = 纯登记票。（digest 链已删，polyforge#149 B9：载荷携带 digest 字段会被忽略，不再落库。）
- **PATCH**（`PATCH /api/v1/work-items/{id}`）：`bundle_name` 为 R2 窗口内可更新键（string 或 null）；`null` = 解绑（票成为登记票，不再出现在 ready queue 任何分段）。digest 键已从可更新键集合删除（polyforge#149 B9）：携带该键的 PATCH 整体拒绝 `CORE-400`（未知/不可更新字段）。
- **落库规则（create 与 PATCH 共用同一代码路径）**：bundle 引用即 `bundle_name` 本身，无 digest（不再派生 stand-in，也不接受显式 pin）；`repo_inventory`（建票时冻结，`reference.md#§2.1`）不受 PATCH 影响。
- **响应面**：WI 视图（GET 详情 / 列表 / queue 条目）携带 `bundle_name`（既有字段，形状不变）。

## 2. R2 窗口语义（首次认领冻结） <!-- §2 -->

- **窗口**：`status='queued'` 且**从未认领**（无活跃 session 行且无 `session_claimed` 事件；force-end 后 session 行清空但认领事件仍在，冻结保持）。窗口内三向 PATCH 均接受：NULL→X（补挂）、X→Y（换名）、X→NULL（解绑）。
- `waiting_on` 非空**不**单独拒绝窗口内 PATCH（是否出现在 ready queue 是队列过滤问题，不是不可变问题）。
- **窗口外拒绝且不写库**：
  - 已认领 / 非 queued → `CORE-400`，`reason=IMMUTABLE`
  - 终态（wrapped / failed / canceled）→ `CORE-410`，`reason=TERMINAL_STATE`
- **对称父子规则**：
  - 有子票（任意子票状态，含已终态）→ PATCH 绑非 NULL bundle 拒绝：`CORE-400`，`reason=HAS_CHILDREN_NO_BUNDLE`（只有 PATCH 会触发：create 出的新票天然无子票；create 侧对应的是下一条 PARENT_NOT_REGISTRY）
  - 已绑 bundle 的票作 parent 的 create 拒绝：`CORE-400`，`reason=PARENT_NOT_REGISTRY`（沿用原语义，无旧名别名）
- **并发（两组；终态一致，被拒的一方得到 typed 拒绝且不写库）**：
  - PATCH bundle vs Claim：不另设仲裁，单写者事务串行化 + 各自的 CAS 保证终态一致。绑定或改名的 PATCH 不会让 Claim 失败（解绑 X→NULL 若排在前，Claim 按无 bundle 票照常被拒 NOT_CLAIMABLE）；PATCH 若排在 Claim 之前则成功（Claim 冻结的是新 bundle），若排在之后则被拒：通常为 `CORE-400 reason=IMMUTABLE`（已有认领），`CORE-409 reason=RACE_LOST` 为防御性兜底（单写者单连接下实际不会出现）。之后的任何 bundle PATCH 都被拒（已冻结）。
  - 绑定 bundle vs 新增子票：创建事务内父票 bundle 复查 + 绑定的守卫写复查子票；终态不同时出现「有子票 ∧ bundle 非空」。
- **Claim 无 bundle 票**仍被拒：`CORE-409`，`reason=NOT_CLAIMABLE`（现行路径不变）。

## 3. ready queue 与父票（响应形状） <!-- §3 -->

- `GET /api/v1/projects/{p}/ready-queue` 响应为 `project` / `items` / `running` / `paused`。无 bundle 的登记票（含 epic，即有子票的票）不出现在任何分段。
- 父票没有派生完成状态：aihub 不自动写终态，子票进度从子票本身读取；子票全部终态后，由 owner 用 `pf wrap` 关单（`POST …/wrap`，无 bundle 的票才可用；仍有非终态子票 → `CORE-409`，`reason=OPEN_CHILDREN`）。
- 存量违规行（bundle 非空且有子票）不迁移、不拒读，按字面数据展示；对称规则只约束新写入。

## 4. 部署与回滚 <!-- §4 -->

- **部署顺序：新 aihub 先于新 pf**。拆分 a 只改 aihub 端；响应只做加法，旧 pf 对新增 JSON 键天然忽略，先部署 aihub 不破坏现行客户端；拆分 b 的客户端改造随后上线。「已绑 bundle 的票不再可作父票」等语义变化属预期，不算回归。
- **回滚**：本票无 schema 变更、无迁移、无新表新列——**回滚仅需回退 aihub 二进制**。数据面无需清理：R2 窗口内发生过的 bundle 变更是普通 UPDATE，回退后按旧只读语义照常展示。
- **上线验证（本地临时 aihub）**：`go test ./...` 全绿（含 w140 验收套件 `test/w140_wi_kind_contract_acceptance_test.go`）；不触碰生产 aihub、不替换 `/usr/local/bin/pf`、不重启 pf daemon。
