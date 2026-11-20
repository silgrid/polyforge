import { Type } from "@sinclair/typebox";

// Daemon-owned fork of the pi subagent extension. The worker definition is
// fixed to the project-scoped worker installed beside this extension.
export default function (pi: any) {
  pi.registerTool({
    name: "subagent",
    label: "subagent",
    description: "Run the daemon-provided worker once, serially.",
    parameters: Type.Object({ task: Type.String() }),
    async execute(_id: string, params: { task: string }) {
      return await pi.exec("pi", ["--mode", "json", "-p", "--no-session", "--no-skills", "--no-context-files", "--no-prompt-templates", "--no-extensions", "--no-approve", "--agent", "worker", params.task]);
    },
  });
}
