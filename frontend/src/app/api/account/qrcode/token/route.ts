// 115 扫码登录 - 获取二维码 token
// 对齐 MoviePilot-Plugins: Api.get_qrcode_api
import { NextRequest, NextResponse } from "next/server";
import { getQrcodeToken, APP_TO_SSOENT, CLIENT_DISPLAY } from "@/lib/115";

export async function GET(req: NextRequest) {
  try {
    const { searchParams } = new URL(req.url);
    const clientType = (searchParams.get("clientType") || "alipaymini").trim();

    // 校验客户端类型
    if (!APP_TO_SSOENT[clientType]) {
      return NextResponse.json(
        {
          error: "无效的客户端类型",
          details: `支持的类型: ${Object.keys(APP_TO_SSOENT).join(", ")}`,
          clientDisplay: CLIENT_DISPLAY,
        },
        { status: 400 }
      );
    }

    const result = await getQrcodeToken(clientType);
    return NextResponse.json({
      success: true,
      ...result,
      clientDisplay: CLIENT_DISPLAY[clientType] || clientType,
    });
  } catch (error) {
    console.error("[API/qrcode/token] Error:", error);
    return NextResponse.json(
      {
        error: "获取二维码失败",
        details: error instanceof Error ? error.message : String(error),
      },
      { status: 500 }
    );
  }
}
