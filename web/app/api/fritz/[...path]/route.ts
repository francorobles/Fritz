// Server-side proxy to the Fritz API on the Pi. The browser never
// sees FRITZ_APP_TOKEN: it is added here. Only the routes the
// dashboard needs pass through.
//
//   FRITZ_API_URL   default http://127.0.0.1:8080
//   FRITZ_APP_TOKEN same value as the Go server's

import { NextRequest } from "next/server";

const API = process.env.FRITZ_API_URL ?? "http://127.0.0.1:8080";

const allowed: Record<string, RegExp> = {
  GET: /^(status|activity|memories)$/,
  DELETE: /^memories\/\d+$/,
};

async function forward(req: NextRequest, params: Promise<{ path: string[] }>) {
  const path = (await params).path.join("/");
  if (!allowed[req.method]?.test(path)) {
    return Response.json({ error: "not allowed" }, { status: 404 });
  }
  const headers: Record<string, string> = {};
  if (process.env.FRITZ_APP_TOKEN) {
    headers.Authorization = `Bearer ${process.env.FRITZ_APP_TOKEN}`;
  }
  try {
    const res = await fetch(`${API}/${path}${req.nextUrl.search}`, {
      method: req.method,
      headers,
      cache: "no-store",
    });
    return new Response(res.status === 204 ? null : await res.text(), {
      status: res.status,
      headers: { "Content-Type": res.headers.get("Content-Type") ?? "application/json" },
    });
  } catch {
    return Response.json({ error: `Fritz API unreachable at ${API}` }, { status: 502 });
  }
}

type Ctx = { params: Promise<{ path: string[] }> };

export async function GET(req: NextRequest, { params }: Ctx) {
  return forward(req, params);
}

export async function DELETE(req: NextRequest, { params }: Ctx) {
  return forward(req, params);
}
