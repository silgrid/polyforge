import { NextResponse } from "next/server";
import { hasJsonContentType, isApiRequestAllowed } from "@/lib/request-security";
import { startComponentsOperation } from "@/lib/components-upgrade";

export const dynamic = "force-dynamic";
// 访问边界：Pi Web 已登录用户，且必须通过同源与 JSON 检查。
export async function POST(request: Request) {
 if(!isApiRequestAllowed(request)) return NextResponse.json({error:"Forbidden"},{status:403});
 if(!hasJsonContentType(request)) return NextResponse.json({error:"需要 application/json"},{status:415});
 try {
  const body:unknown=await request.json();
  await startComponentsOperation("upgrade",body);
  return NextResponse.json({ok:true},{status:202});
 } catch(error) {
  return NextResponse.json({error:error instanceof Error?error.message:"无法启动组件操作"},{status:400});
 }
}
