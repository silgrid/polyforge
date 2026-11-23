import type { ComponentsStatus } from "./components-upgrade";

// 每次直接读取被断言的最终状态；网络断线不终止轮询，关闭面板后立即解除定时器。
export async function pollComponentsStatus(read:()=>Promise<ComponentsStatus>, receive:(value:ComponentsStatus)=>void, signal:AbortSignal, interval=2000):Promise<void> {
 while(!signal.aborted) {
  try { const value=await read();if(!signal.aborted) receive(value); } catch { /* 重连后继续从卷读取。 */ }
  if(signal.aborted) return;
  await new Promise<void>(resolve=> {
   const done=()=>{clearTimeout(timer);signal.removeEventListener("abort",done);resolve();};
   const timer=setTimeout(done,interval);signal.addEventListener("abort",done,{once:true});
  });
 }
}
