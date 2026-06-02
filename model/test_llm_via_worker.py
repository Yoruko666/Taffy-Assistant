"""LLM 单测脚本（经 Worker，绕过 ASR）。

用法：
    # 先启动 worker（另一窗口）
    #   cd worker; $env:LLM_API_KEY="sk-xxx"; go run ./cmd/worker
    python model/test_llm_via_worker.py
    python model/test_llm_via_worker.py "卧室灯调暗"

直连 worker 的 /v1/orchestrate，发 text_input 帧（跳过 ASR），观察：
    - asr_final  ：worker 回显文本
    - device_command（如果触发 tool_call）
    - llm_result ：最终文本回复
"""
import asyncio
import json
import sys

import websockets

WS_URL = "ws://127.0.0.1:8090/v1/orchestrate?device_id=dev-test"

CASES = [
    "打开客厅灯",
    "把空调调到26度",
    "今天天气怎么样",
]


async def one_round(text: str):
    print(f"\n=== 用户：{text} ===")
    try:
        async with websockets.connect(WS_URL, max_size=None) as ws:
            await ws.send(json.dumps({"type": "text_input", "text": text}))

            # 等待最多 30s 拿到 llm_result
            try:
                async with asyncio.timeout(30):
                    while True:
                        msg = await ws.recv()
                        if isinstance(msg, bytes):
                            print(f"  [audio] {len(msg)} bytes")
                            continue
                        data = json.loads(msg)
                        t = data.get("type")
                        if t == "asr_final":
                            print(f"  [asr_final] {data.get('text')}")
                        elif t == "device_command":
                            print(f"  [device_command] {json.dumps(data, ensure_ascii=False)}")
                        elif t == "llm_result":
                            print(f"  [llm_result] {data.get('text')}")
                            return
                        elif t == "error":
                            print(f"  [error] {data}")
                            return
                        else:
                            print(f"  [{t}] {data}")
            except asyncio.TimeoutError:
                print("  [TIMEOUT] 30s 内没拿到 llm_result")
    except Exception as e:
        print(f"  [WS ERROR] {e}")


async def main():
    if len(sys.argv) > 1:
        await one_round(" ".join(sys.argv[1:]))
    else:
        for c in CASES:
            await one_round(c)


if __name__ == "__main__":
    asyncio.run(main())
