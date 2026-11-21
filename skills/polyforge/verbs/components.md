# components — 工作站容器内更新

仅用于携带只读种子 manifest 的工作站镜像；宿主机使用 tools/install.sh。
所有命令为 Host 级，不传 --profile。

- `pf components check`：查询 current、渠道最新版与可更新状态。
- `pf components status`：读取 current、previous 和 upgrade-status.json。
- `pf components upgrade --version V`：下载并核对 SHA256SUMS，准备完整版本，排空后切换。
- `pf components upgrade --seed-dir D --version V`：使用完整本地种子。
- `pf components rollback`：交换 current 与 previous，拒绝不兼容 schema、平台、渠道、底座版本或已跨过的数据布局（previous 声明的 dataLayout 与数据目录 layout-version 不符）。
- `pf components cancel`：取消等待并请求 daemon 重启，清除排空。

upgrade 与 rollback 支持 `--force`，省略 upgrade 的版本时使用渠道最新版。
排空期间新作业收到 DAEMON-409；已有作业结束后自动继续。
组件卷的 upgrade-status.json 持久保存结果与原因，Pi Web 重启后重新读取。
退出码 75 让 supervise 重新解析 current 拉起 daemon；0/78 保持原义。

pf（含 daemon/AIHub）、车道与 skill、Pi Web 可在容器内升级；harness 暂时携带
原版本，node、python、git、Podman、supervise 与底座需要换镜像。
数据 schema 迁移按各版本说明处理，回退前必须备份并确认兼容性。
