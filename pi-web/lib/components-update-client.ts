import type { ComponentsStatus } from "./components-upgrade";
export async function pollComponentsStatus(_read:()=>Promise<ComponentsStatus>, _receive:(value:ComponentsStatus)=>void, _signal:AbortSignal, _interval=2000):Promise<void> {}
