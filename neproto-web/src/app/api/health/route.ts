import { NextResponse } from "next/server";

export const dynamic = "force-dynamic";

export function GET() {
  return NextResponse.json(
    {
      service: "neproto-web",
      status: "ok",
      version: process.env.NEPROTO_VERSION ?? "development",
    },
    {
      headers: {
        "Cache-Control": "no-store",
      },
    },
  );
}
