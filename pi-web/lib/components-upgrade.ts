import { spawn, execFile } from "node:child_process";
import { readFile } from "node:fs/promises";
import { join } from "node:path";
import { workstationImage } from "./workstation-mode.ts";

export interface ComponentsStatus {
 imageManaged: boolean;
 current: string | null;
 previous: string | null;
 latest?: string | null;
 status: { state: string; phase?: string; from?: string; to?: string; error?: string; operation?: string } | null;
}
export interface ComponentsOptions { root?: string; seedRoot?: string; executable?: string; spawnProcess?: typeof spawn }

export async function readComponentsStatus(options: ComponentsOptions = {}): Promise<ComponentsStatus> {
 const root=options.root ?? "/components";
 const imageManaged=workstationImage(options.seedRoot);
 if(!imageManaged) return {imageManaged,current:null,previous:null,status:null};
 const version=async (link:string):Promise<string|null>=> {
  try { return (JSON.parse(await readFile(join(root,link,"manifest.json"),"utf8")) as {version:string}).version; }
  catch(error) { if((error as NodeJS.ErrnoException).code==="ENOENT") return null;throw error; }
 };
 let status:ComponentsStatus["status"]=null;
 try { status=JSON.parse(await readFile(join(root,"upgrade-status.json"),"utf8")); }
 catch(error) { if((error as NodeJS.ErrnoException).code!=="ENOENT") throw error; }
 return {imageManaged,current:await version("current"),previous:await version("previous"),status};
}

// 仅由已通过 Pi Web 登录与同源检查的操作路由调用，CLI 自身再检查镜像身份。
export async function startComponentsOperation(operation: string, body: unknown, options: ComponentsOptions = {}): Promise<void> {
 if(!workstationImage(options.seedRoot)) throw new Error("仅工作站镜像支持组件升级");
 if(!["upgrade","rollback","cancel"].includes(operation)) throw new Error("未知组件操作");
 if(!body || typeof body!=="object" || Array.isArray(body)) throw new Error("操作参数必须是对象");
 const input=body as {version?:unknown;force?:unknown};
 if(input.version!==undefined && (typeof input.version!=="string" || !/^v?\d+\.\d+\.\d+(?:-(?:dev|gmi)\.\d+)?$/.test(input.version))) throw new Error("无效版本号");
 if(input.force!==undefined && typeof input.force!=="boolean") throw new Error("force 必须为布尔值");
 const args=["components",operation];
 if(operation==="upgrade" && input.version) args.push("--version",input.version as string);
 if(operation!=="cancel" && input.force) args.push("--force");
 const {sanitizeProjectCommandEnvironment}=await import("./project-command-env.ts");
 const env=sanitizeProjectCommandEnvironment(process.env);
 await new Promise<void>((resolve,reject)=> {
  const child=(options.spawnProcess ?? spawn)(options.executable ?? "pf",args,{detached:true,stdio:"ignore",shell:false,cwd:"/",env});
  child.once("error",reject);
  child.once("spawn",()=>{child.unref();resolve();});
 });
}

export async function checkComponentsLatest(): Promise<string|null> {
 return new Promise((resolve,reject)=>execFile("pf",["--json","components","check"],{timeout:10000},(error,stdout)=> {
  if(error) {reject(error);return;}
  try {resolve(JSON.parse(stdout).latest ?? null);} catch(error) {reject(error);}
 }));
}
