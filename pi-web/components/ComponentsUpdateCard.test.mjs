// carries: components/ComponentsUpdateCard.tsx, components/PolyforgeConfig.tsx, lib/components-update-client.ts, lib/polyforge-detect.ts, app/api/app-update/route.ts
import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { createJiti } from "jiti";
const jiti=createJiti(import.meta.url,{jsx:{runtime:"automatic"},tsconfigPaths:true});
const React=await jiti.import("react");
const {renderToStaticMarkup}=await jiti.import("react-dom/server");
const {ComponentsUpdateView}=await jiti.import("./ComponentsUpdateCard.tsx");
const {pollComponentsStatus}=await jiti.import("../lib/components-update-client.ts");
const snapshot={imageManaged:true,current:"v0.2.5",previous:"v0.2.4",latest:"v0.2.6",status:{state:"done",to:"v0.2.5"}};
test("组件卡片：显示最终结果、版本与回退入口",()=>{
 for(const state of ["done","failed","waiting-drain","running"]) {
  const html=renderToStaticMarkup(React.createElement(ComponentsUpdateView,{snapshot:{...snapshot,status:{state,to:"v0.2.5",error:state==="failed"?"校验失败":""}},error:null,onAction:()=>{}}));
  assert.match(html,/v0.2.5/);assert.match(html,/v0.2.4/);assert.match(html,/回到上一版/);
  assert.match(html,state==="done"?/升级成功/:state==="failed"?/校验失败/:state==="waiting-drain"?/等待作业结束/:/正在更新/);
  // r2#1：等待中显示「取消并重启 daemon」（真实行为）；运行中的恢复入口换
  // 成与行为一致的「恢复中断的编排」——是否恢复由 CLI 按锁探测结果决定。
  if(state==="waiting-drain") assert.match(html,/取消并重启 daemon/);
  if(state==="running") {assert.match(html,/恢复中断的编排/);assert.doesNotMatch(html,/取消并重启 daemon/);}
 }
});
test("组件卡片：断线后自动重读并呈现卷上的最终结果",async()=>{
 const controller=new AbortController();let calls=0;const seen=[];
 const settled=new Promise((resolve,reject)=> {
  const timer=setTimeout(()=>{controller.abort();reject(Error("未重连到最终结果"));},1000);
  pollComponentsStatus(async()=>{calls++;if(calls===1)throw Error("断线");return snapshot;},value=>{seen.push(value);if(value.status.state==="done"){clearTimeout(timer);controller.abort();resolve();}},controller.signal,1).catch(reject);
 });
 await settled;assert.equal(calls,2);assert.equal(seen.at(-1).status.to,"v0.2.5");
});
test("组件卡片：宿主模式不显示容器升级操作",()=>{
 const html=renderToStaticMarkup(React.createElement(ComponentsUpdateView,{snapshot:{...snapshot,imageManaged:false},error:null,onAction:()=>{}}));assert.equal(html,"");
});
