import { readdirSync, statSync } from "node:fs";
import { join } from "node:path";

// 镜像身份只由只读种子 manifest 确定，组件卷本身不是身份标记。
export function workstationImage(seedRoot = "/opt/polyforge/seed"): boolean {
 try {
  return readdirSync(join(seedRoot,"releases")).some(version => {
   try { return statSync(join(seedRoot,"releases",version,"manifest.json")).isFile(); } catch { return false; }
  });
 } catch { return false; }
}
