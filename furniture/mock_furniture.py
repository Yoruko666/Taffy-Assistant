"""Mock 家具端（PC 端模拟器） ———— 实时麦克风 → VAD → WS 上行测试。

M2 行为变更：
  - VAD 检测到句尾 end 后，暂停语音检测，等待大模型返回结果（llm_result）；
  - 收到 llm_result 后恢复语音检测，继续下一轮对话。

用法
----
    # 实时麦克风
    python furniture/mock_furniture.py --server ws://127.0.0.1:8080/v1/voice?device_id=dev1&token=t1

    # 列出音频设备
    python furniture/mock_furniture.py --list-devices
"""

from __future__ import annotations

import argparse
import asyncio
import base64
import json
import sys
from datetime import datetime
from pathlib import Path
from abc import ABC, abstractmethod
from typing import Optional

try:
    import numpy as np
    import webrtcvad
    import websockets
except ImportError as exc:
    print(f"[mock-furniture] 缺少依赖：{exc}")
    print("请先：pip install -r furniture/requirements.txt")
    sys.exit(1)


# ---------------------------------------------------------------------------
# 常量
# ---------------------------------------------------------------------------

SAMPLE_RATE = 16000
SAMPLE_WIDTH = 2
VAD_FRAME_MS = 30
VAD_FRAME_BYTES = SAMPLE_RATE * VAD_FRAME_MS * SAMPLE_WIDTH // 1000  # 960
UPLINK_CHUNK_MS = 600
UPLINK_CHUNK_BYTES = SAMPLE_RATE * UPLINK_CHUNK_MS * SAMPLE_WIDTH // 1000  # 19200
VAD_FRAME_SAMPLES = VAD_FRAME_BYTES // SAMPLE_WIDTH  # 480


# ---------------------------------------------------------------------------
# KWS 抽象层
# ---------------------------------------------------------------------------


class WakeWord(ABC):
    @abstractmethod
    def feed(self, frame_pcm16: bytes) -> bool: ...

    @abstractmethod
    def is_active_window(self) -> bool: ...


class AlwaysOnWakeWord(WakeWord):
    def feed(self, frame_pcm16: bytes) -> bool:
        return False

    def is_active_window(self) -> bool:
        return True


# ---------------------------------------------------------------------------
# VAD 端点检测器
# ---------------------------------------------------------------------------


class VadSegmenter:
    def __init__(self, aggressiveness: int, silence_ms: int, min_speech_ms: int):
        self._vad = webrtcvad.Vad(aggressiveness)
        self._silence_frames_to_end = max(1, silence_ms // VAD_FRAME_MS)
        self._speech_frames_to_start = max(1, min_speech_ms // VAD_FRAME_MS)
        self._speaking = False
        self._silence_run = 0
        self._speech_run = 0

    @property
    def speaking(self) -> bool:
        return self._speaking

    def reset(self) -> None:
        """VAD 在一段话结束后清空状态。"""
        self._speaking = False
        self._silence_run = 0
        self._speech_run = 0

    def feed(self, frame_pcm16: bytes) -> str:
        if len(frame_pcm16) != VAD_FRAME_BYTES:
            return ""
        is_speech = self._vad.is_speech(frame_pcm16, SAMPLE_RATE)
        if self._speaking:
            if is_speech:
                self._silence_run = 0
            else:
                self._silence_run += 1
                if self._silence_run >= self._silence_frames_to_end:
                    self._speaking = False
                    self._silence_run = 0
                    self._speech_run = 0
                    return "end"
        else:
            if is_speech:
                self._speech_run += 1
                if self._speech_run >= self._speech_frames_to_start:
                    self._speaking = True
                    self._speech_run = 0
                    self._silence_run = 0
                    return "start"
            else:
                self._speech_run = 0
        return ""


# ---------------------------------------------------------------------------
# WS 客户端
# ---------------------------------------------------------------------------


async def receiver(
    ws,
    stop: asyncio.Event,
    llm_done: asyncio.Event,
    output_dir: Path,
) -> None:
    """接收服务端事件，输出简洁的对话格式。

    用户说话结束 → 打印 `用户：<asr_final文本>`
    大模型返回  → 打印 `小菲：<llm_result文本>`
    """
    try:
        async for msg in ws:
            if isinstance(msg, bytes):
                continue
            try:
                evt = json.loads(msg)
            except json.JSONDecodeError:
                continue
            t = evt.get("type", "?")
            cur = evt.get("text", "")

            if t in ("final", "asr_final"):
                print(f"用户：{cur}")
            elif t in ("llm_result", "reply"):
                if cur:
                    print(f"小菲：{cur}")
                llm_done.set()
            elif t == "llm_error":
                err_msg = evt.get("message", cur)
                print(f"小菲：出错了（{err_msg}）")
                llm_done.set()
            elif t == "tts_audio":
                b64_data = evt.get("data", "")
                if b64_data:
                    raw = base64.b64decode(b64_data)
                    ts = datetime.now().strftime("%Y%m%d_%H%M%S")
                    out_path = output_dir / f"tts_{ts}.wav"
                    out_path.write_bytes(raw)
                    print(f"[tts] saved: {out_path}")
            elif t == "device_done":
                device_name = evt.get("device", "?")
                action = evt.get("action", "?")
                print(f"[device] {device_name} {action} done")
            elif t in ("pong", "eos"):
                pass
            elif t == "error":
                err_msg = evt.get("message", "")
                print(f"[error] {err_msg}")
    except websockets.ConnectionClosed:
        pass
    finally:
        stop.set()


async def send_start(ws) -> None:
    await ws.send(json.dumps({
        "type": "start",
        "sample_rate": SAMPLE_RATE,
        "format": "pcm_s16le",
        "channels": 1,
    }))


async def send_end(ws) -> None:
    await ws.send(json.dumps({"type": "end"}))


async def stream_live(
    ws,
    wakeword: WakeWord,
    vad: VadSegmenter,
    llm_done: asyncio.Event,
    device: Optional[str] = None,
    silence_ms: int = 800,
) -> int:
    """从真实麦克风实时采集，VAD 断句后推送到 WS。

    M2 行为：
      - VAD 检测到 end 后暂停语音检测；
      - 等待 llm_done 事件（由 receiver 在收到 llm_result 时设置）；
      - 恢复语音检测，进入下一轮对话。
    """
    try:
        import sounddevice as sd
    except ImportError:
        print("[mock-furniture] 需安装 sounddevice：pip install sounddevice")
        sys.exit(1)

    from collections import deque

    q: "asyncio.Queue[bytes]" = asyncio.Queue(maxsize=100)

    def mic_callback(indata: "np.ndarray", frames: int, _time, status) -> None:
        if status:
            print(f"[mock-furniture] mic status: {status}", file=sys.stderr)
        mono = indata.mean(axis=1) if indata.shape[1] > 1 else indata[:, 0]
        pcm16 = np.clip(mono, -1.0, 1.0)
        pcm16 = (pcm16 * 32767).astype("<i2")
        try:
            q.put_nowait(pcm16.tobytes())
        except asyncio.QueueFull:
            try:
                q.get_nowait()
                q.put_nowait(pcm16.tobytes())
            except asyncio.QueueEmpty:
                pass

    device_id: Optional[int] = None
    if device:
        if device.isdigit():
            device_id = int(device)
        else:
            for idx, dev in enumerate(sd.query_devices()):
                if device.lower() in dev["name"].lower() and dev["max_input_channels"] > 0:
                    device_id = idx
                    break
            if device_id is None:
                print(f"[mock-furniture] 未找到匹配 '{device}' 的输入设备")
                return 0

    print("[mock-furniture] mic is live — speak now, Ctrl+C to stop")
    print("-" * 60)
    stream = sd.InputStream(
        samplerate=SAMPLE_RATE,
        channels=1,
        dtype="float32",
        callback=mic_callback,
        blocksize=VAD_FRAME_SAMPLES,
        device=device_id,
    )
    stream.start()

    end_count = 0
    seg_buf = bytearray()
    lookback_frames = max(0, 300 // VAD_FRAME_MS)
    pre_buf: "deque[bytes]" = deque(maxlen=lookback_frames)

    # 语音检测暂停标记：大模型处理中不检测新语音
    vad_paused = False

    async def flush_segment_buf() -> None:
        nonlocal seg_buf
        if seg_buf:
            await ws.send(bytes(seg_buf))
            seg_buf = bytearray()

    try:
        while True:
            try:
                frame = await asyncio.wait_for(q.get(), timeout=1.0)
            except asyncio.TimeoutError:
                if not stream.active:
                    break
                continue

            # ---- 暂停检测：不处理音频，只等待 llm_done ----
            if vad_paused:
                # 等待大模型返回（检查事件是否已被设置）
                try:
                    await asyncio.wait_for(llm_done.wait(), timeout=300)
                except asyncio.TimeoutError:
                    # 超时保护：防止永远阻塞
                    print("\n[warning] LLM 等待超时，强制恢复语音检测")
                llm_done.clear()
                vad_paused = False
                vad.reset()
                print("-" * 60)
                continue

            wakeword.feed(frame)
            if not wakeword.is_active_window():
                continue

            evt = vad.feed(frame)
            if evt == "start":
                await send_start(ws)
                seg_buf = bytearray()
                for pf in pre_buf:
                    seg_buf.extend(pf)
                pre_buf.clear()
            elif evt == "end":
                await flush_segment_buf()
                await send_end(ws)
                end_count += 1
                # M2：end 后暂停语音检测，等大模型结果
                vad_paused = True
                continue  # 不把当前帧计入 speaking 处理

            if vad.speaking:
                seg_buf.extend(frame)
                if len(seg_buf) >= UPLINK_CHUNK_BYTES:
                    await flush_segment_buf()
            else:
                pre_buf.append(frame)
    except asyncio.CancelledError:
        pass
    finally:
        stream.stop()
        stream.close()

    if vad.speaking:
        await flush_segment_buf()
        await send_end(ws)
        end_count += 1

    return end_count


# ---------------------------------------------------------------------------
# 主流程
# ---------------------------------------------------------------------------


async def main_async(args: argparse.Namespace) -> int:
    if args.list_devices:
        try:
            import sounddevice as sd
        except ImportError:
            print("[mock-furniture] 需安装 sounddevice：pip install sounddevice")
            return 1
        print("[mock-furniture] 可用音频输入设备：")
        print(sd.query_devices())
        return 0

    url = args.server
    try:
        ws = await websockets.connect(url, max_size=None, open_timeout=10)
    except Exception as e:
        print(f"[mock-furniture] connect failed: {e!r}")
        return 3

    stop = asyncio.Event()
    # llm_done：receiver 收到 llm_result 后设置，stream_live 等待后继续
    llm_done = asyncio.Event()
    output_dir = Path(__file__).resolve().parent / "output"
    output_dir.mkdir(parents=True, exist_ok=True)
    recv_task = asyncio.create_task(receiver(ws, stop, llm_done, output_dir))

    try:
        wakeword = AlwaysOnWakeWord()
        vad = VadSegmenter(aggressiveness=2, silence_ms=800, min_speech_ms=90)
        await stream_live(ws, wakeword, vad, llm_done, device=args.device)

        try:
            await asyncio.wait_for(stop.wait(), timeout=15)
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

    return 0


def parse_args() -> argparse.Namespace:
    p = argparse.ArgumentParser(
        description="家具端实时麦克风测试：麦克风 → VAD → WS 上行到 server"
    )
    p.add_argument("--server", default="ws://127.0.0.1:8080/v1/voice?device_id=dev1&token=t1",
                   help="目标 WS 地址（含鉴权参数）")
    p.add_argument("--list-devices", action="store_true",
                   help="列出可用音频输入设备并退出")
    p.add_argument("--device", default=None,
                   help="音频输入设备编号或名称关键词（默认用系统默认设备）")
    return p.parse_args()


if __name__ == "__main__":
    try:
        rc = asyncio.run(main_async(parse_args()))
    except KeyboardInterrupt:
        print("\n[mock-furniture] interrupted")
        rc = 130
    sys.exit(rc)
