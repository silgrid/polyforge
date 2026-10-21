# 08 — Capsule 基底迁移运行手册（人工流程）<!-- 权威 -->

> **注意 / Note（2026-10-12）**：本文部分内容已被 [v2-converge.md](v2-converge.md) 取代；两者冲突时以 v2-converge 为准。Parts of this document are superseded by v2-converge.md; where they conflict, v2-converge wins.

| 权威项 | 值 |
|---|---|
| 文档定位 | Capsule 基底（workspace）的人工迁移流程：换机/换盘/迁移到新基底时的shutdown → 映射改写 → 重启序列（进行中的 capsule 不搬）、`capsules/` 的 `.gitignore` 指引 |
| 本文是唯一权威 | Daemon **没有**、也永远不会有自动迁移：没有迁移子命令、没有迁移旗标、启动时不搬任何 capsule。本文是唯一一个命名旧基底默认路径的地方 |

## 1. 背景 <!-- §1 -->

Daemon 的 Capsule 基底自 polyforge#113 起由**共享配置的 `[workspace."<path>"]` 映射**唯一决定：
每节一个 workspace（`projects = [...]` 列出落点在该 workspace 的 projects；键首 `~` 展开为家目录，
键必须为绝对路径、不带尾部分隔符；一个 project 只能属于一个 workspace——逆映射必须是函数）。
CLI 与 Daemon 读同一份配置；**不存在任何启动期 workspace 旗标或环境变量**（历史形状
`-workspace` 旗标与其环境镜像已删除：传旧旗标就是普通的「未知旗标」解析失败，配置里的平面
`project` 键会被拒绝加载并给出迁移指引；旧版更早的缺省基底 `<data-dir>/workspace` 与
`-workspace-root`/`POLYFORGE_WORKSPACE_ROOT` 一并早已删除）：

- **配置含零个 `[workspace]` 节**（新装机模板形状）时 Daemon 拒绝启动，指向该形状与 `pf doctor`。
- Daemon 启动时校验**每一个**已映射 workspace：目录必须**已存在**（Daemon 绝不代建它），且
  `<workspace>/capsules/` 可创建、可写。Daemon 在任一 workspace 内只写 `capsules/` 子树。
- Capsule 按被认领 WI 的 project 经逆映射路由到 `<workspace>/capsules/<project>-<seq>/`（polyforge#144；
  此前认领的 capsule 目录名为 `<wi_id>`，按记录路径续跑）。
- **换基不需要搬进行中的 capsule**：把旧基底作为 workspace 节（连同它的 projects）保留在映射里，
  进行中的 capsule 就「留在原地」继续有效——重启对账按记录里的 capsule 路径就地续跑，绝不搬迁、
  绝不从远端 git 链重建（未发布的本地 capsule 状态不允许被静默丢弃）。
- 运行中的 WI 若其 project 已从映射中消失（操作者删节后重启），Daemon 不再整体拒绝启动：只把**该 WI**
  在 aihub 置为 `blocked`（原因注记点名 project，`pf watch` 显示 `project_unmapped` 事件），其余 WI 照常恢复；
  capsule **绝不**被搬迁到别的 workspace。补救：把该 project 的 workspace 节重新加回映射，再 `pf resume <id>`
  （一次从 aihub capsule 链与 carrier ref 的全新再认领，见 v2-converge §14.1）。
- 终态（已 wrap/fail/cancel）的 capsule 留在原地不动；Daemon 工具面（`pf watch` 等）只在该 WI 的 project
  **映射到的** workspace 里按 `capsules/<project>-<seq>` 寻找 capsule——把旧基底（连同该 project）重新加回映射即可让
  它们继续找到它。（原文此处举的 `pf ship` 已由 polyforge#166 删除：交付改为 workflow 的 deliver 步骤。）

## 2. 迁移序列（人工，按序执行）<!-- §2 -->

### 2a. 换机/换盘：进行中的 capsule 不搬 <!-- §2a -->

重启对账只认 job 记录里的 capsule 路径（`runtime/restart.go` `requireMidflightCapsule`）：该 WI 的 project
仍在映射里（任一 workspace）且记录的目录存在，就原地续跑。所以**不要 `mv` 进行中的 capsule**——搬走后记录的
路径不存在，WI 按 `capsule_missing` 置 blocked，搬过去的目录不会被使用。旧目录无法保留（换机、拆盘）时：

```
# 0. 目标 workspace 先建好（Daemon 不代建它）
mkdir -p /srv/polyforge/workspace

# 1. 停掉本机 daemon（干净退出同时结束 `pf daemon ensure` 的 tmux 监督环，不会被重新拉起）
pf ops shutdown
# （自管 systemd 单元的机器：systemctl stop polyforge-daemon）

# 2. 改写共享配置的 [workspace] 映射：新基底一节，列出 projects
#    （编辑 ~/.config/polyforge/cli.toml）
#      [workspace."/srv/polyforge/workspace"]
#      projects = ["p1", "p2"]

# 3. 用同一份配置重新拉起 daemon（映射就是唯一的 workspace 来源，无旗标可传；polyforge#174）
pf daemon ensure

# 4. 旧目录已不在的进行中 WI 此时是 blocked（capsule_missing）：从 aihub 重新领取到新基底
pf resume <id>
#    capsule 链 + carrier ref（refs/pf-task/<name>）+ WIP ref（refs/pf-wip/<name>）恢复；
#    已完成的步骤不重跑，最后一次推送之后的本地改动丢失
```

### 2b. 换基但不搬家：旧基底保留为映射节 <!-- §2b -->

旧目录留在原处即可：进行中的 capsule 按记录路径原地续跑（只要求其 project 仍在映射里，落在哪一节都行）。
若旧基底的 projects 还要继续在那里落新 capsule，就把它保留为映射里的一节：

```
#  [workspace."/old/ws"]
#  projects = ["legacy-proj"]
#
#  [workspace."/srv/polyforge/workspace"]
#  projects = ["p1", "p2"]
```

### 2c. 核对 <!-- §2c -->

重启后用 `pf ops status` 核对 `workspaces:` 一行列出映射里的**全部** workspace（不再只报一个）；
保留在原处的进行中 WI 在重启对账时原地续跑，旧目录已不在的 WI 是 blocked、等 `pf resume`；`pf doctor` 逐项校验映射
（缺目录、project 未知、逆映射歧义都会被点名）。

## 3. `capsules/` 的 `.gitignore` 指引 <!-- §3 -->

workspace 常位于 git 仓库或被 git 跟踪的目录内。**Daemon 从不创建、追加或改写
`<workspace>/.gitignore`**——忽略 `capsules/` 是装机者/操作者自己的动作：

```
# 在 workspace 的 .gitignore 里加一行：
capsules/
```

`tools/install.sh` 会在安装时替你加（并打印它做了什么）；按本手册手工迁移的机器请自行补上。
