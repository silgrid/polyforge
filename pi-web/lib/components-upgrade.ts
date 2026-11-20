import { spawn, type SpawnOptions } from "node:child_process";

export interface ComponentsStatus {
 imageManaged: boolean;
 current: string | null;
 previous: string | null;
 latest?: string | null;
 status: { state: string; phase?: string; from?: string; to?: string; error?: string; operation?: string } | null;
}
export interface ComponentsOptions { root?: string; seedRoot?: string; executable?: string; spawnProcess?: typeof spawn }
export async function readComponentsStatus(_options: ComponentsOptions = {}): Promise<ComponentsStatus> {
 return {imageManaged:false,current:null,previous:null,status:null};
}
export async function startComponentsOperation(_operation: string, _body: unknown, _options: ComponentsOptions = {}): Promise<void> {
 throw new Error("尚未实现");
}
