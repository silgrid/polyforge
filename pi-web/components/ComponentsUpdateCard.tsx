"use client";
import { useEffect, useState } from "react";
import type { ComponentsStatus } from "@/lib/components-upgrade";
import { pollComponentsStatus } from "@/lib/components-update-client";
import { ConfigButton } from "./SettingsUi";

export function ComponentsUpdateView({snapshot,error,onAction}:{snapshot:ComponentsStatus|null;error:string|null;onAction:(operation:string,force?:boolean)=>void}) {
 if(!snapshot?.imageManaged) return null;
 const state=snapshot.status?.state;
 const busy=state==="running" || state==="waiting-drain";
 return <section aria-label="容器组件更新" className="polyforge-config-card">
  <h3>容器组件更新</h3>
  <p>当前版本：{snapshot.current ?? "未知"} · 可用版本：{snapshot.latest ?? "待检查"} · 上一版：{snapshot.previous ?? "无"}</p>
  <p role="status">{state==="done"?"升级成功":state==="failed"?"升级失败":state==="waiting-drain"?"等待作业结束":state==="cancelled"?"已取消":busy?"正在更新":"准备就绪"} {snapshot.status?.to}</p>
  {snapshot.status?.error && <p role="alert">{snapshot.status.error}</p>}
  {error && <p role="alert">{error}</p>}
  <ConfigButton size="small" disabled={busy} onClick={()=>onAction("upgrade")}>更新</ConfigButton>
  <ConfigButton size="small" disabled={busy || !snapshot.previous} onClick={()=>onAction("rollback")}>回到上一版</ConfigButton>
  {state==="waiting-drain" && <ConfigButton size="small" onClick={()=>onAction("cancel")}>取消并重启 daemon</ConfigButton>}
  {state==="running" && <ConfigButton size="small" onClick={()=>onAction("cancel")}>恢复中断的编排</ConfigButton>}
  {state==="waiting-drain" && <ConfigButton size="small" variant="danger" onClick={()=>onAction(snapshot.status?.operation ?? "upgrade",true)}>强制更新</ConfigButton>}
 </section>;
}

export function ComponentsUpdateCard({active}:{active:boolean}) {
 const [snapshot,setSnapshot]=useState<ComponentsStatus|null>(null);
 const [error,setError]=useState<string|null>(null);
 useEffect(()=> {
  if(!active) return;
  const controller=new AbortController();let first=true;
  void pollComponentsStatus(async()=> {
   const response=await fetch(`/api/components/status${first?"?check=1":""}`,{signal:AbortSignal.any([controller.signal,AbortSignal.timeout(12000)])});
   if(!response.ok) throw Error("等待更新服务重新连接");
   first=false;return await response.json() as ComponentsStatus;
  },value=>{setSnapshot(old=>({...value,latest:value.latest ?? old?.latest}));setError(null);},controller.signal);
  return ()=>controller.abort();
 },[active]);
 const action=async(operation:string,force=false)=> {
  try {
   const response=await fetch(`/api/components/${operation}`,{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({force})});
   if(!response.ok) {const body=await response.json();throw Error(body.error ?? "无法启动更新");}
  } catch(error) {setError(error instanceof Error?error.message:"无法启动更新");}
 };
 return <ComponentsUpdateView snapshot={snapshot} error={error} onAction={(operation,force)=>{void action(operation,force);}}/>;
}
