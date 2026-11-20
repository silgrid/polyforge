import { NextResponse } from "next/server";
import { isApiRequestAllowed } from "@/lib/request-security";
import { readComponentsStatus, checkComponentsLatest } from "@/lib/components-upgrade";
export const dynamic = "force-dynamic";
export async function GET(request: Request) {
 if(!isApiRequestAllowed(request)) return NextResponse.json({error:"Forbidden"},{status:403});
 try {
  const result=await readComponentsStatus();
  if(result.imageManaged && new URL(request.url).searchParams.get("check")==="1") {
   try {result.latest=await checkComponentsLatest();} catch {result.latest=null;}
  }
  return NextResponse.json(result);
 } catch(error) {
  return NextResponse.json({error:error instanceof Error?error.message:"无法读取组件状态"},{status:503});
 }
}
