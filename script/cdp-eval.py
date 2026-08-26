#!/usr/bin/env python3
"""CDP の各ページに Runtime.evaluate を打ち、式の結果を tab 番号つきで出す。

使い方: cdp.py <cdp-port> <origin-substring> <js-expression>
"""
import asyncio, json, re, sys, urllib.request
import websockets


def targets(port, origin):
    with urllib.request.urlopen(f"http://127.0.0.1:{port}/json/list", timeout=5) as r:
        ts = json.load(r)
    out = []
    for t in ts:
        if t.get("type") == "page" and origin in t.get("url", ""):
            m = re.search(r"tab=(\d+)", t["url"])
            out.append((int(m.group(1)) if m else 0, t["webSocketDebuggerUrl"]))
    return sorted(out)


async def evaluate(ws_url, expr):
    try:
        async with websockets.connect(ws_url, open_timeout=5, max_size=None) as ws:
            await ws.send(json.dumps({
                "id": 1, "method": "Runtime.evaluate",
                "params": {"expression": expr, "returnByValue": True, "awaitPromise": True},
            }))
            while True:
                msg = json.loads(await asyncio.wait_for(ws.recv(), timeout=30))
                if msg.get("id") == 1:
                    res = msg.get("result", {})
                    if "exceptionDetails" in res:
                        return "EXCEPTION: " + res["exceptionDetails"].get("text", "")
                    return res.get("result", {}).get("value")
    except Exception as exc:  # 接続できないタブも結果として残す
        return f"UNREACHABLE: {type(exc).__name__}: {exc}"


async def main():
    port, origin, expr = sys.argv[1], sys.argv[2], sys.argv[3]
    ts = targets(port, origin)
    results = await asyncio.gather(*(evaluate(ws, expr) for _, ws in ts))
    for (n, _), v in zip(ts, results):
        print(f"  tab={n} -> {v!r}")
    print(f"  タブ数={len(ts)}")


asyncio.run(main())
