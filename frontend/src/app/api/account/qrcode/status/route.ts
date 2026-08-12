// 115 扫码登录 - 查询扫码状态
// 对齐 MoviePilot-Plugins: Api.check_qrcode_api
import { NextRequest, NextResponse } from "next/server";
import { getQrcodeStatus, APP_TO_SSOENT } from "@/lib/115";

export async function GET(req: NextRequest) {
  try {
    const { searchParams } = new URL(req.url);
    const uid = searchParams.get("uid") || "";
    const time = searchParams.get("time") || "";
    const sign = searchParams.get("sign") || "";
    const clientType = (searchParams.get("clientType") || "alipaymini").trim();

    if (!uid || !time || !sign) {
      return NextResponse.json(
        { error: "uid, time, sign 参数不能为空" },
        { status: 400 }
      );
    }

    if (!APP_TO_SSOENT[clientType]) {
      return NextResponse.json(
        { error: "无效的客户端类型" },
        { status: 400 }
      );
    }

    const result = await getQrcodeStatus(uid, time, sign, clientType);
    return NextResponse.json({ success: true, ...result });
  } catch (error) {
    console.error("[API/qrcode/status] Error:", error);
    return NextResponse.json(
      {
        error: "查询扫码状态失败",
        details: error instanceof Error ? error.message : String(error),
      },
      { status: 500 }
    );
  }
}
