// carries: lib/components-upgrade.ts, app/api/components/**
import "#test-env";
import test from "node:test";
import assert from "node:assert/strict";
import { mkdtemp, mkdir, writeFile, symlink, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { EventEmitter } from "node:events";
import { readComponentsStatus, startComponentsOperation } from "./components-upgrade.ts";

async function fixture(t) {
 const root=await mkdtemp(join(tmpdir(),"components-ui-")); t.after(()=>rm(root,{recursive:true,force:true}));
 const seedRoot=join(root,"seed");
 await mkdir(join(seedRoot,"releases","v0.2.4"),{recursive:true});
 await writeFile(join(seedRoot,"releases","v0.2.4","manifest.json"),"{}");
 for(const version of ["v0.2.4","v0.2.5"]) {
  await mkdir(join(root,"releases",version),{recursive:true});
  await writeFile(join(root,"releases",version,"manifest.json"),JSON.stringify({version}));
 }
 await symlink("releases/v0.2.5",join(root,"current"));
 await symlink("releases/v0.2.4",join(root,"previous"));
 return {root,seedRoot};
}
test("组件升级：重启后从卷读取版本与成功或失败结果",async t=>{
 const options=await fixture(t);
 for(const state of ["done","failed"]) {
  await writeFile(join(options.root,"upgrade-status.json"),JSON.stringify({state,to:"v0.2.5",error:state==="failed"?"SHA256 校验失败":""}));
  const actual=await readComponentsStatus(options);
  assert.equal(actual.imageManaged,true);
  assert.equal(actual.current,"v0.2.5"); assert.equal(actual.previous,"v0.2.4");
  assert.equal(actual.status.state,state); assert.equal(actual.status.to,"v0.2.5");
  if(state==="failed") assert.equal(actual.status.error,"SHA256 校验失败");
 }
});
test("组件升级：脱离 Pi Web 会话启动且不继承密码",async t=>{
 const options=await fixture(t); let call; let unref=false;
 const spawnProcess=(...args)=>{call=args;const child=new EventEmitter();child.unref=()=>{unref=true};queueMicrotask(()=>child.emit("spawn"));return child;};
 process.env.PI_WEB_PASSWORD="测试密码";t.after(()=>delete process.env.PI_WEB_PASSWORD);
 await startComponentsOperation("upgrade",{version:"v0.2.6",force:true},{...options,spawnProcess});
 assert.deepEqual(call[1],["components","upgrade","--version","v0.2.6","--force"]);
 assert.equal(call[2].detached,true); assert.equal(call[2].stdio,"ignore"); assert.equal(call[2].shell,false);
 assert.equal(call[2].env.PI_WEB_PASSWORD,undefined);assert.equal(unref,true);
});
test("组件升级：宿主、非法操作和非法版本均不启动进程",async t=>{
 const options=await fixture(t);let starts=0;const spawnProcess=()=>{starts++;throw Error("不应启动")};
 await assert.rejects(startComponentsOperation("upgrade",{}, {seedRoot:join(options.root,"absent"),spawnProcess}),/工作站/);
 await assert.rejects(startComponentsOperation("delete",{}, {...options,spawnProcess}),/操作/);
 await assert.rejects(startComponentsOperation("upgrade",{version:"--help"},{...options,spawnProcess}),/版本/);
 assert.equal(starts,0);
});
test("组件升级：启动失败必须返回错误",async t=>{
 const options=await fixture(t);
 const spawnProcess=()=>{const child=new EventEmitter();queueMicrotask(()=>child.emit("error",Error("ENOENT")));return child;};
 await assert.rejects(startComponentsOperation("rollback",{}, {...options,spawnProcess}),/ENOENT/);
});
