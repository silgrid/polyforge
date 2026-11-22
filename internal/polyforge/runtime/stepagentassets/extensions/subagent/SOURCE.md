# subagent extension — source, version and license

## 来源与固定身份

本目录的 `index.ts` / `agents.ts` 是 daemon fork。它的**实际上游基线**是 operator agent
目录里的 subagent 扩展快照（构建时从 `/root/.pi/agent/extensions/subagent` 取得，文件时间
2026-09-19；快照本身不携带版本号）：

- 上游快照 `index.ts` sha256 `3493514708d71de722b26494f23b77258e32c75cc7c74302ac96b7db139eb4b5`；
- 上游快照 `agents.ts` sha256 `cc85ecd33b89aa575bea3d2a9a9da7654a6305da4de02e5041aa5342a1de29b4`；
- 本目录 fork 的 `index.ts` sha256 `843449a370423cb1088f9462805a5b6d62018920008455be0d70aa395a43ced4`；
  `agents.ts` 与上游快照**逐字节相同**（`cc85ecd3…`）。

**同宿主的版本对照（不是本 fork 的基线）**：构建宿主安装的
`@earendil-works/pi-coding-agent` 版本是 **1.4.0**（npm 包 `package.json` 的 `version`；
仓库 `https://github.com/earendil-works/pi` 的 `packages/coding-agent`）。该版本随包发布的
`examples/extensions/subagent/index.ts` sha256 是
`1d11858d163e2a3a258af51c1923fc55ad825813acd6224462fd25ad61f5401b`
（同目录的 `agents.ts` 仍是 `cc85ecd3…`）。它与上面的 operator 快照**不是同一份**：
1.4.0 的示例文件改用 `formatReadLineRange` 并去掉了 `isPiCliScript`，而本 fork 仍使用
`isPiCliScript`、不引用 `formatReadLineRange`。因此本目录的上游基线是 2026-09-19 的
operator 快照，**不是** 1.4.0 随包示例文件；1.4.0 在这里只作同宿主版本对照，将来若把
fork 迁到 1.4.0 的示例文件，需另行核对并改这份说明。

校验值以本目录文件的实际字节为准：`internal/contracttest/pi_subagent_proof_docs_contract_test.go`
会把本目录两个文件的 sha256 重新计算后与上表比对，并 pin 上表四个常量（fork、快照、
1.4.0 示例、`agents.ts`）不漂移；文件再改动时本文件必须同改。上游快照与 1.4.0 示例不在
仓内，contract test 只能 pin 它们的字面值，路径与推导依据就是本节。

## 本地修改（相对上游）

daemon 只改输入边界，不改输出协议形状：

- 固定 `worker`：只接受 `agent: "worker"` 单任务；拒绝其他 agent 名、`tasks`（parallel）与
  `chain`，以及 `agentScope` 为 `project` / `both`；
- 隔离启动：子进程 argv 追加 `--no-skills --no-context-files --no-prompt-templates
  --no-extensions --no-approve`；
- 每次调用带 `PF_SUBAGENT_ID` 继承标记，中止/超时按标记停掉子进程及其后代；
- `executionMode: "sequential"`（一次一个 `worker`），`timeoutSeconds` 正整数校验与默认 1800。

## 许可证

上游以 **MIT** 发布：`@earendil-works/pi-coding-agent` 的 `package.json` 声明
`"license": "MIT"`、`"author": "Mario Zechner"`，仓库为
`https://github.com/earendil-works/pi`。该 npm 包内**没有**随附独立的 LICENSE 文件；
下面的正文是标准 MIT 文本，著作权人取自 `package.json` 的 `author` 字段，**不是**从包内
某个 LICENSE 文件逐字复制。上游仓库根目录的 LICENSE 文件是该许可的权威文本，可按上面的
仓库地址核对。

```
MIT License

Copyright (c) Mario Zechner

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```
