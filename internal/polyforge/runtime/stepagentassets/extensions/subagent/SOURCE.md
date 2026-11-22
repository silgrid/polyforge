# subagent extension — source, version and license

上游固定版本：`@earendil-works/pi-coding-agent` **1.4.0**（npm 包 `package.json` 的
`version`；仓库 `https://github.com/earendil-works/pi` 的 `packages/coding-agent`）。
本目录的 `index.ts` / `agents.ts` 取自该版本随包发布的 operator subagent 扩展快照
（构建时从 operator agent 目录 `/root/.pi/agent/extensions/subagent` 取得）：

- `agents.ts` 与上游快照**逐字节相同**，sha256 `cc85ecd33b89aa575bea3d2a9a9da7654a6305da4de02e5041aa5342a1de29b4`。
- `index.ts` 是 daemon fork，sha256 `843449a370423cb1088f9462805a5b6d62018920008455be0d70aa395a43ced4`（上游快照
  sha256 `3493514708d71de722b26494f23b77258e32c75cc7c74302ac96b7db139eb4b5`，用于对照）。

校验值以本目录文件的实际字节为准；`internal/contracttest/pi_subagent_proof_docs_contract_test.go`
会把这两个 sha256 与本目录文件重新计算的值比对，文件再改动时本文件必须同改。

## 本地修改（相对上游）

daemon 只改输入边界，不改输出协议形状：

- 固定 `worker`：只接受 `agent: "worker"` 单任务；拒绝其他 agent 名、`tasks`（parallel）与
  `chain`，以及 `agentScope` 为 `project` / `both`；
- 隔离启动：子进程 argv 追加 `--no-skills --no-context-files --no-prompt-templates
  --no-extensions --no-approve`；
- 每次调用带 `PF_SUBAGENT_ID` 继承标记，中止/超时按标记停掉子进程及其后代；
- `executionMode: "sequential"`（一次一个 `worker`），`timeoutSeconds` 正整数校验与默认 1800。

## 许可证

上游以 **MIT License** 发布（`@earendil-works/pi-coding-agent` 的 `package.json`：
`"license": "MIT"`，`"author": "Mario Zechner"`）。本目录的派生文件沿用该许可，原文如下：

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
