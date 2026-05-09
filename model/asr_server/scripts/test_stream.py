"""模拟家具端：把一个 wav 文件按 600ms 切帧，通过 WebSocket 发送，打印识别结果。

用途：
  - 联调 ASR Model（直连 ws://127.0.0.1:9100/v1/asr/stream）
  - 联调 Go Server 透传（ws://127.0.0.1:8080/v1/voice?device_id=...&token=...）

不需要真实家具端硬件。

依赖：
  pip install websockets soundfile numpy

用法：
  # 1) 直连 ASR Model（验证模型层 OK）
  python test_stream.py --url ws://127.0.0.1:9100/v1/asr/stream --wav test.wav

  # 2) 经 Go Server（验证透传层 OK）
  python test_stream.py --url "ws://127.0.0.1:8080/v1/voice?device_id=dev1&token=t1" --wav test.wav

  # 3) 自定义帧长（默认 600ms，与 ASR chunk_size=[0,10,5] 对齐）
  python test_stream.py --url ws://... --wav test.wav --frame-ms 600

  # 4) 不发 end，靠 VAD 自动断句（要求 ASR 加载了 fsmn-vad）
  python test_stream.py --url ws://... --wav test.wav --no-end --hold 5
"""

from __future__ import annotations

import argparse
import asyncio
import json
import sys
import time
from pathlib import Path

try:
    import numpy as np
    import soundfile as sf
    import websockets
except ImportError as e:  # noqa: BLE001
    print(f"[mock-furniture] 缺少依赖：{e}\n请先：pip install websockets soundfile numpy")
    sys.exit(1)


SAMPLE_RATE = 16000


def load_wav_as_pcm16_mono(path: Path) -> bytes:
    """把任意 wav 转为 16k mono 16-bit LE PCM 字节流。"""
    data, sr = sf.read(str(path), dtype="float32", always_2d=False)
    if data.ndim == 2:  # 多声道 -> 取均值
        data = data.mean(axis=1)
    # 重采样到 16k（简单线性插值，够联调用）
    if sr != SAMPLE_RATE:
        ratio = SAMPLE_RATE / sr
        new_len = int(len(data) * ratio)
        idx = (np.arange(new_len) / ratio).astype(np.int64)
        idx = np.clip(idx, 0, len(data) - 1)
        data = data[idx]
    # float32 [-1,1] -> int16
    pcm16 = np.clip(data, -1.0, 1.0)
    pcm16 = (pcm16 * 32767).astype("<i2")  # little-endian
    return pcm16.tobytes()


async def receiver(ws: "websockets.WebSocketClientProtocol", stop: asyncio.Event) -> None:
    """打印服务端推回的所有事件，直到 final/eos/error 或连接断开。"""
    try:
        async for msg in ws:
            if isinstance(msg, bytes):
                print(f"<- (binary {len(msg)} bytes)")
                continue
            try:
                e = json.loads(msg)
            except json.JSONDecodeError:
                print(f"<- (raw) {msg!r}")
                continue

            t = e.get("type", "?")
            if t in ("partial", "asr_partial"):
                print(f"<- {t}: {e.get('text', '')}")
            elif t in ("final", "asr_final"):
                print(f"<- {t}: {e.get('text', '')}  ✅")
            elif t == "ready":
                print(f"<- ready")
            elif t == "eos":
                print(f"<- eos")
                stop.set()
                return
            elif t == "error":
                print(f"<- error: {e.get('message', '')}  ❌")
                stop.set()
                return
            else:
                # 上游链路（reply / device_done / tts_audio）也打印
                preview = {k: (v if k != "data" else f"<{len(v)}B base64>") for k, v in e.items()}
                print(f"<- {t}: {preview}")
    except websockets.ConnectionClosed as exc:
        print(f"[mock-furniture] connection closed: {exc.code} {exc.reason}")
    finally:
        stop.set()


async def sender(
    ws: "websockets.WebSocketClientProtocol",
    pcm: bytes,
    frame_ms: int,
    send_end: bool,
    realtime: bool,
) -> None:
    """发 start / 切帧 PCM / end。"""
    # 1) start
    start_msg = {
        "type": "start",
        "sample_rate": SAMPLE_RATE,
        "format": "pcm_s16le",
        "channels": 1,
    }
    await ws.send(json.dumps(start_msg))
    print("[mock-furniture] sent start frame")

    # 2) 切帧
    frame_bytes = frame_ms * SAMPLE_RATE * 2 // 1000
    total = len(pcm)
    sent = 0
    n_frames = 0
    t0 = time.time()
    while sent < total:
        chunk = pcm[sent : sent + frame_bytes]
        sent += len(chunk)
        n_frames += 1
        await ws.send(chunk)
        if n_frames % 5 == 0 or sent >= total:
            print(f"[mock-furniture] sent {n_frames} PCM frames ({sent}/{total} B)")
        # 模拟实时录音节奏，避免一次性灌爆服务端
        if realtime:
            await asyncio.sleep(frame_ms / 1000.0)

    elapsed = time.time() - t0
    print(f"[mock-furniture] all PCM sent: {n_frames} frames in {elapsed:.2f}s")

    # 3) end
    if send_end:
        await ws.send(json.dumps({"type": "end"}))
        print("[mock-furniture] sent end frame")


async def main_async(args: argparse.Namespace) -> int:
    wav_path = Path(args.wav)
    if not wav_path.exists():
        print(f"[mock-furniture] wav not found: {wav_path}")
        return 2

    print(f"[mock-furniture] loading wav: {wav_path}")
    pcm = load_wav_as_pcm16_mono(wav_path)
    duration_ms = len(pcm) // 2 * 1000 // SAMPLE_RATE
    print(f"[mock-furniture] pcm: {len(pcm)} bytes ≈ {duration_ms} ms")

    print(f"[mock-furniture] connecting {args.url}")
    try:
        ws = await websockets.connect(args.url, max_size=None, open_timeout=10)
    except Exception as e:  # noqa: BLE001
        print(f"[mock-furniture] connect failed: {e!r}")
        return 3
    print("[mock-furniture] connected")

    stop = asyncio.Event()
    recv_task = asyncio.create_task(receiver(ws, stop))
    try:
        await sender(
            ws,
            pcm=pcm,
            frame_ms=args.frame_ms,
            send_end=not args.no_end,
            realtime=args.realtime,
        )
        # 给服务端时间产出 final / eos / 下游事件
        try:
            await asyncio.wait_for(stop.wait(), timeout=args.hold)
        except asyncio.TimeoutError:
            print(f"[mock-furniture] hold timeout reached ({args.hold}s), exiting")
    finally:
        try:
            await ws.close()
        except Exception:
            pass
        try:
            await asyncio.wait_for(recv_task, timeout=2)
        except (asyncio.TimeoutError, asyncio.CancelledError):
            recv_task.cancel()

    print("[mock-furniture] done")
    return 0


def parse_args() -> argparse.Namespace:
    p = argparse.ArgumentParser(
        description="模拟家具端：通过 WebSocket 把 wav 文件喂给 ASR / Go Server 做联调"
    )
    p.add_argument(
        "--url",
        default="ws://127.0.0.1:9100/v1/asr/stream",
        help="目标 WS 地址。直连 ASR：默认；经 Go Server：ws://host:8080/v1/voice?device_id=...&token=...",
    )
    p.add_argument("--wav", required=True, help="要发送的音频文件（任意采样率，会自动重采样到 16k mono）")
    p.add_argument("--frame-ms", type=int, default=600, help="每帧时长（毫秒），默认 600")
    p.add_argument(
        "--no-end",
        action="store_true",
        help="不发 end 帧（依赖 VAD 自动断句，要求 ASR 加载了 fsmn-vad）",
    )
    p.add_argument(
        "--hold",
        type=float,
        default=10.0,
        help="发完 PCM 后等待服务端响应的最大秒数，默认 10",
    )
    p.add_argument(
        "--realtime",
        action="store_true",
        help="按帧间隔模拟实时录音节奏（更真实，速度更慢）",
    )
    return p.parse_args()


if __name__ == "__main__":
    rc = asyncio.run(main_async(parse_args()))
    sys.exit(rc)
