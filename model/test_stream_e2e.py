"""端到端流式测试：TTS 合成 -> 直接切帧喂给流式 ASR -> 打印识别过程。

相比 test_e2e.py 的差别：
    - test_e2e.py  走 ASR 的 REST /transcribe（整段识别，一次返回）
    - 本脚本      走 ASR 的 WebSocket /v1/asr/stream（流式，逐帧 partial -> final）

用途：
    - 模拟真实家具端链路（边说边识别）
    - 批量验证多条指令的识别准确率和延迟
    - 不落盘中间 wav（除非加 --save）

用法：
    python test_stream_e2e.py
    python test_stream_e2e.py --text "把空调调到26度"
    python test_stream_e2e.py --realtime                  # 按真实节奏发帧
    python test_stream_e2e.py --batch                     # 跑一组预置指令

依赖：requests websockets numpy soundfile
"""
from __future__ import annotations

import argparse
import asyncio
import io
import json
import sys
import time
from pathlib import Path
from typing import List, Tuple

try:
    import numpy as np
    import requests
    import soundfile as sf
    import websockets
except ImportError as e:  # noqa: BLE001
    print(f"缺少依赖：{e}\n请先：pip install requests websockets soundfile numpy")
    sys.exit(1)


TTS_URL = "http://127.0.0.1:9200/v1/tts/synthesize"
ASR_WS = "ws://127.0.0.1:9100/v1/asr/stream"
SAMPLE_RATE = 16000

PRESET_PHRASES = [
    "把客厅的灯打开",
    "关掉卧室的灯",
    "把空调调到二十六度",
    "播放一首轻音乐",
    "把窗帘拉上一半",
    "现在几点了",
]


# ---------- TTS ----------
def tts_synthesize(text: str) -> bytes:
    """调 TTS 返回 wav bytes。"""
    t0 = time.time()
    r = requests.post(TTS_URL, json={"text": text, "format": "wav"}, timeout=30)
    r.raise_for_status()
    audio = r.content
    print(f"  [TTS] {len(audio)} bytes, {(time.time() - t0) * 1000:.0f} ms")
    return audio


def wav_bytes_to_pcm16_mono(wav_bytes: bytes) -> bytes:
    """wav bytes -> 16k mono 16-bit LE PCM。"""
    data, sr = sf.read(io.BytesIO(wav_bytes), dtype="float32", always_2d=False)
    if data.ndim == 2:
        data = data.mean(axis=1)
    if sr != SAMPLE_RATE:
        ratio = SAMPLE_RATE / sr
        new_len = int(len(data) * ratio)
        idx = (np.arange(new_len) / ratio).astype(np.int64)
        idx = np.clip(idx, 0, len(data) - 1)
        data = data[idx]
    pcm16 = np.clip(data, -1.0, 1.0)
    pcm16 = (pcm16 * 32767).astype("<i2")
    return pcm16.tobytes()


# ---------- ASR 流式 ----------
async def stream_asr(pcm: bytes, frame_ms: int, realtime: bool, hold: float) -> Tuple[str, List[str], float, float]:
    """把 pcm 切帧发给流式 ASR，返回 (final_text, partial_list, first_partial_ms, total_ms)。"""
    partials: List[str] = []
    final_text = ""
    first_partial_t: float = -1.0
    t_start = time.time()

    ws = await websockets.connect(ASR_WS, max_size=None, open_timeout=10)
    stop = asyncio.Event()

    async def receiver() -> None:
        nonlocal final_text, first_partial_t
        try:
            async for msg in ws:
                if isinstance(msg, bytes):
                    continue
                try:
                    e = json.loads(msg)
                except json.JSONDecodeError:
                    continue
                t = e.get("type", "")
                if t in ("partial", "asr_partial"):
                    txt = e.get("text", "")
                    if txt:
                        if first_partial_t < 0:
                            first_partial_t = time.time()
                        partials.append(txt)
                elif t in ("final", "asr_final"):
                    final_text = e.get("text", "") or final_text
                elif t == "eos":
                    stop.set()
                    return
                elif t == "error":
                    print(f"  [ASR] error: {e.get('message', '')}")
                    stop.set()
                    return
        except websockets.ConnectionClosed:
            pass
        finally:
            stop.set()

    recv_task = asyncio.create_task(receiver())

    try:
        # start
        await ws.send(json.dumps({
            "type": "start",
            "sample_rate": SAMPLE_RATE,
            "format": "pcm_s16le",
            "channels": 1,
        }))

        # 切帧
        frame_bytes = frame_ms * SAMPLE_RATE * 2 // 1000
        total = len(pcm)
        sent = 0
        while sent < total:
            chunk = pcm[sent: sent + frame_bytes]
            sent += len(chunk)
            await ws.send(chunk)
            if realtime:
                await asyncio.sleep(frame_ms / 1000.0)

        # end
        await ws.send(json.dumps({"type": "end"}))

        try:
            await asyncio.wait_for(stop.wait(), timeout=hold)
        except asyncio.TimeoutError:
            pass
    finally:
        try:
            await ws.close()
        except Exception:
            pass
        try:
            await asyncio.wait_for(recv_task, timeout=2)
        except (asyncio.TimeoutError, asyncio.CancelledError):
            recv_task.cancel()

    total_ms = (time.time() - t_start) * 1000
    first_partial_ms = (first_partial_t - t_start) * 1000 if first_partial_t > 0 else -1.0
    return final_text, partials, first_partial_ms, total_ms


# ---------- 单条测试 ----------
def _safe_name(text: str, idx: int) -> str:
    """把文本转成安全的 Windows 文件名（保留中文，去掉非法字符、截断）。"""
    bad = '<>:"/\\|?*'
    cleaned = "".join(c for c in text if c not in bad).strip()
    if len(cleaned) > 40:
        cleaned = cleaned[:40]
    return f"{idx:02d}_{cleaned}" if cleaned else f"{idx:02d}"


async def run_one(
    text: str,
    idx: int,
    frame_ms: int,
    realtime: bool,
    hold: float,
    save_dir: Path | None,
) -> dict:
    print(f"\n>>> [{text}]")
    wav_bytes = tts_synthesize(text)
    save_path: Path | None = None
    if save_dir is not None:
        save_dir.mkdir(parents=True, exist_ok=True)
        save_path = save_dir / f"{_safe_name(text, idx)}.wav"
        save_path.write_bytes(wav_bytes)
        print(f"  [save] {save_path}")

    pcm = wav_bytes_to_pcm16_mono(wav_bytes)
    duration_ms = len(pcm) // 2 * 1000 // SAMPLE_RATE
    print(f"  [pcm] {len(pcm)} bytes ≈ {duration_ms} ms")

    final_text, partials, first_partial_ms, total_ms = await stream_asr(
        pcm, frame_ms=frame_ms, realtime=realtime, hold=hold,
    )

    # partial 节奏：只打印去重后的变化
    shown: List[str] = []
    for p in partials:
        if not shown or p != shown[-1]:
            shown.append(p)
    for p in shown:
        print(f"  [partial] {p}")
    print(f"  [final]   {final_text}")
    if first_partial_ms > 0:
        print(f"  [timing]  first_partial={first_partial_ms:.0f} ms, total={total_ms:.0f} ms")
    else:
        print(f"  [timing]  (no partial received), total={total_ms:.0f} ms")

    ok = (text in final_text) or (final_text and final_text in text)
    print(f"  [verdict] {'PASS' if ok else 'DIFF'}")
    return {
        "text": text,
        "final": final_text,
        "ok": ok,
        "first_partial_ms": first_partial_ms,
        "total_ms": total_ms,
        "wav": str(save_path) if save_path else "",
    }


# ---------- 主入口 ----------
async def main_async(args: argparse.Namespace) -> int:
    # 默认保存到 audio_dump/<时间戳>/，用 --no-save 关闭，用 --save-dir 指定位置
    save_dir: Path | None
    if args.no_save:
        save_dir = None
    else:
        base = Path(args.save_dir) if args.save_dir else Path(__file__).parent / "audio_output"
        stamp = time.strftime("%Y%m%d_%H%M%S")
        save_dir = base / stamp

    if args.batch:
        phrases = PRESET_PHRASES
    else:
        phrases = [args.text]

    results = []
    for i, p in enumerate(phrases, start=1):
        try:
            r = await run_one(p, i, args.frame_ms, args.realtime, args.hold, save_dir)
            results.append(r)
        except Exception as e:  # noqa: BLE001
            print(f"  [ERROR] {e!r}")
            results.append({"text": p, "ok": False, "error": repr(e)})

    # 汇总
    print("\n=== Summary ===")
    ok_cnt = sum(1 for r in results if r.get("ok"))
    print(f"PASS: {ok_cnt}/{len(results)}")
    for r in results:
        flag = "PASS" if r.get("ok") else "DIFF"
        fp = r.get("first_partial_ms", -1)
        tot = r.get("total_ms", -1)
        print(f"  [{flag}] {r['text']!r:30}  first_partial={fp:.0f}ms  total={tot:.0f}ms  got={r.get('final','')!r}")

    if save_dir is not None:
        print(f"\n[wav] 保存目录：{save_dir}")
        print(f"[wav] 播放全部：Get-ChildItem '{save_dir}' *.wav | ForEach-Object {{ Start-Process $_.FullName; Start-Sleep 2 }}")
        print(f"[wav] 打开目录：explorer '{save_dir}'")

    return 0 if ok_cnt == len(results) else 1


def parse_args() -> argparse.Namespace:
    p = argparse.ArgumentParser(description="TTS -> 流式 ASR 端到端测试")
    p.add_argument("--text", default="把客厅的灯打开", help="单条测试文本")
    p.add_argument("--batch", action="store_true", help="跑一组预置指令（忽略 --text）")
    p.add_argument("--frame-ms", type=int, default=600, help="每帧毫秒（默认 600，对齐 chunk_size=[0,10,5]）")
    p.add_argument("--realtime", action="store_true", help="按真实节奏发帧（每帧间 sleep frame_ms）")
    p.add_argument("--hold", type=float, default=10.0, help="发完 end 后等服务端回 eos 的最大秒数")
    p.add_argument("--save-dir", default="", help="自定义保存目录根路径（不含时间戳子目录）")
    p.add_argument("--no-save", action="store_true", help="不保存中间 wav")
    return p.parse_args()


if __name__ == "__main__":
    sys.exit(asyncio.run(main_async(parse_args())))
