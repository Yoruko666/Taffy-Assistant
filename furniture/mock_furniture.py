"""Mock 家具端（PC 端模拟器） —— 家具助手"小菲"的 PC 虚拟实现。

本脚本模拟一台带麦克风的智能家居设备（音箱 / 屏幕设备），跑在普通 PC 上，
**不需要真实麦克风**。音源由一个或多个 wav 文件拼成"虚拟音频流"，
在文件之间插入静音段，模拟用户说一句、停一会、再说一句的真实节奏。

整体行为复刻主流智能音箱的端侧链路：

    [虚拟麦克风 wav 流]
            │
            ▼
    [KWS 唤醒词检测]   ← 抽象层；M2 默认实现 = AlwaysOn（始终视为已唤醒）
            │
            ▼
    [VAD 端点检测]     ← webrtcvad，检测到说话开始/结束 → 自动发 start/end
            │
            ▼
    [WebSocket 上行]   ── 16k PCM ──▶  Go Server (/v1/voice) ──▶  ASR Model
            │
            ▼
    [接收 asr_partial / asr_final 滚屏打印]

设计要点
---------
1. **断句归家具，不归 ASR**：webrtcvad 在本机做端点检测，检测到持续静音
   ≥ ``--silence-ms`` (默认 800ms) 就发 ``end`` 帧给 server，触发一次 ``asr_final``。
2. **支持多句连说**：可同时传多个 wav，脚本会把它们拼起来，每句之间塞
   ``--gap-ms`` (默认 1200ms) 的静音，模拟用户两次说话之间的停顿。
   每段语音独立产生一对 ``start`` / ``end``，得到一个 ``asr_final``。
3. **WS 长连接复用**：一次连接、多段语音，与真实家具行为一致。
4. **KWS 预留**：``WakeWord`` 抽象基类 + ``AlwaysOnWakeWord`` 默认实现。
   M3 接入 openWakeWord / Porcupine 时，只需新增一个子类，无需动主流程。

依赖
----
    pip install -r furniture/requirements.txt
    # 等价于：pip install websockets webrtcvad soundfile numpy

用法
----
    # 1) 单句 + 经 server 透传
    python furniture/mock_furniture.py \
        --server ws://127.0.0.1:8080/v1/voice \
        --device-id dev1 --token t1 \
        --wav model/audio_output/20260509_203301/01_把客厅的灯打开.wav

    # 2) 多句连说，模拟用户连续交互
    python furniture/mock_furniture.py \
        --server ws://127.0.0.1:8080/v1/voice \
        --wav a.wav b.wav c.wav --gap-ms 1500

    # 3) PTT 模式（关闭 VAD，等价老 test_stream.py）
    python furniture/mock_furniture.py --mode ptt --wav a.wav

    # 4) 直连 ASR（绕过 server，便于排查）
    python furniture/mock_furniture.py \
        --server ws://127.0.0.1:9100/v1/asr/stream --wav a.wav
"""

from __future__ import annotations

import argparse
import asyncio
import json
import sys
import time
from abc import ABC, abstractmethod
from pathlib import Path
from typing import List, Optional

try:
    import numpy as np
    import soundfile as sf
    import webrtcvad
    import websockets
except ImportError as exc:  # noqa: BLE001
    print(f"[mock-furniture] 缺少依赖：{exc}")
    print("请先：pip install -r furniture/requirements.txt")
    sys.exit(1)


# ---------------------------------------------------------------------------
# 常量
# ---------------------------------------------------------------------------

SAMPLE_RATE = 16000
SAMPLE_WIDTH = 2  # int16 = 2 bytes
# webrtcvad 只支持 10 / 20 / 30 ms 帧，且采样率必须是 8/16/32/48k。
# 取 30ms = 480 samples = 960 bytes，兼顾灵敏度与稳定性。
VAD_FRAME_MS = 30
VAD_FRAME_BYTES = SAMPLE_RATE * VAD_FRAME_MS * SAMPLE_WIDTH // 1000  # 960
# 推给 server 的 PCM 块大小（与 ASR chunk_size=[0,10,5] 对齐 ≈ 600ms）
UPLINK_CHUNK_MS = 600
UPLINK_CHUNK_BYTES = SAMPLE_RATE * UPLINK_CHUNK_MS * SAMPLE_WIDTH // 1000  # 19200


# ---------------------------------------------------------------------------
# 工具：wav → 16k mono PCM16
# ---------------------------------------------------------------------------


def _resample_to_16k(data: "np.ndarray", sr: int) -> "np.ndarray":
    """把 float32 单声道音频重采样到 16 kHz。

    优先级：
        1) ``scipy.signal.resample_poly`` —— 多相滤波，自带抗混叠低通，效果最好。
        2) 朴素整数倍抽取（仅当 sr 是 16k 的整数倍）+ 简易 FIR 低通。
        3) 线性插值回退 —— 会混叠，仅作保底，强烈建议装 scipy。

    为什么不能用"索引抽样"那种线性插值：
        22.05k / 44.1k -> 16k 时，> 8 kHz 的分量会折返到 0–8 kHz 区间
        （aliasing），导致中文音素被破坏，ASR 输出像 "马听德德灯打" 这样的
        乱串。必须先做抗混叠低通再抽样。
    """
    if sr == SAMPLE_RATE:
        return data

    # ---- 方案 1：scipy（推荐） ----
    try:
        from math import gcd

        from scipy.signal import resample_poly  # type: ignore

        g = gcd(int(sr), SAMPLE_RATE)
        up = SAMPLE_RATE // g
        down = sr // g
        return resample_poly(data, up, down).astype("float32", copy=False)
    except ImportError:
        pass

    # ---- 方案 2：soxr（可选，比 scipy 更快更准） ----
    try:
        import soxr  # type: ignore

        return soxr.resample(data, sr, SAMPLE_RATE).astype("float32", copy=False)
    except ImportError:
        pass

    # ---- 方案 3：线性插值保底（有混叠，仅联调兜底） ----
    print(
        "[mock-furniture] ⚠ 未安装 scipy/soxr，使用线性插值重采样，"
        "ASR 结果可能明显退化。建议：pip install scipy",
        file=sys.stderr,
    )
    ratio = SAMPLE_RATE / sr
    new_len = int(len(data) * ratio)
    idx = (np.arange(new_len) / ratio).astype(np.int64)
    idx = np.clip(idx, 0, len(data) - 1)
    return data[idx]


def load_wav_as_pcm16_mono(path: Path) -> bytes:
    """把任意 wav 转为 16k / mono / 16-bit LE PCM 字节流。"""
    data, sr = sf.read(str(path), dtype="float32", always_2d=False)
    if data.ndim == 2:
        data = data.mean(axis=1)  # 多声道 -> 取均值
    if sr != SAMPLE_RATE:
        data = _resample_to_16k(data, sr)
    pcm16 = np.clip(data, -1.0, 1.0)
    pcm16 = (pcm16 * 32767).astype("<i2")
    return pcm16.tobytes()


def silence_pcm(ms: int) -> bytes:
    """生成指定时长的静音 PCM。"""
    n_samples = SAMPLE_RATE * ms // 1000
    return b"\x00\x00" * n_samples


def build_virtual_mic_stream(wavs: List[Path], gap_ms: int, lead_ms: int = 500) -> bytes:
    """把多个 wav 拼成"连续麦克风流"，文件之间插入静音段。

    - ``lead_ms``：流开头的静音（让 VAD 有一段稳定的"非语音基线"）
    - ``gap_ms``：每段语音之间的静音（模拟用户两句话之间的停顿）
    - 末尾再补一段静音，让最后一句的"end"能被 VAD 触发
    """
    parts: List[bytes] = [silence_pcm(lead_ms)]
    for i, w in enumerate(wavs):
        parts.append(load_wav_as_pcm16_mono(w))
        if i < len(wavs) - 1:
            parts.append(silence_pcm(gap_ms))
    parts.append(silence_pcm(max(gap_ms, 1000)))  # 收尾静音
    return b"".join(parts)


# ---------------------------------------------------------------------------
# KWS 抽象层（M3 替换点）
# ---------------------------------------------------------------------------


class WakeWord(ABC):
    """唤醒词检测抽象。每来一个 30ms 音频帧调用一次 ``feed``。"""

    @abstractmethod
    def feed(self, frame_pcm16: bytes) -> bool:
        """返回 True 表示"刚刚检测到唤醒词，进入会话窗口"。"""

    @abstractmethod
    def is_active_window(self) -> bool:
        """是否处于"已唤醒、可接收用户语音"的活跃窗口内。"""


class AlwaysOnWakeWord(WakeWord):
    """默认实现：永远视为已唤醒（M2 阶段 = 进程启动即可说话）。

    相当于小菲唤醒后没有超时，一直处于会话窗口。
    """

    def feed(self, frame_pcm16: bytes) -> bool:  # noqa: ARG002
        return False  # 不会"触发新唤醒"，但 is_active_window 永远 True

    def is_active_window(self) -> bool:
        return True


# 预留：将来加 OpenWakeWordEngine(WakeWord) / PorcupineWakeWord(WakeWord)
# 在那里实现 ONNX / Porcupine 推理，触发后开 N 秒会话窗口即可。


# ---------------------------------------------------------------------------
# VAD 端点检测器
# ---------------------------------------------------------------------------


class VadSegmenter:
    """基于 webrtcvad 的端点检测，按 30ms 帧驱动，给出"段开始 / 段结束"事件。

    状态机：
        IDLE  --(连续 N 帧语音)-->  SPEAKING
        SPEAKING --(连续 silence_ms 静音)--> IDLE
    """

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

    def feed(self, frame_pcm16: bytes) -> str:
        """喂一个 30ms 帧，返回 "start" / "end" / ""。"""
        if len(frame_pcm16) != VAD_FRAME_BYTES:
            return ""  # 末尾不足一帧，丢弃
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
# WS 客户端：上行 PCM、下行事件
# ---------------------------------------------------------------------------


async def receiver(ws, stop: asyncio.Event) -> None:
    """打印 server / ASR 推回的所有事件。"""
    try:
        async for msg in ws:
            if isinstance(msg, bytes):
                print(f"<- (binary {len(msg)} bytes)")
                continue
            try:
                evt = json.loads(msg)
            except json.JSONDecodeError:
                print(f"<- (raw) {msg!r}")
                continue
            t = evt.get("type", "?")
            if t in ("partial", "asr_partial"):
                print(f"   ↳ {t}: {evt.get('text', '')}")
            elif t in ("final", "asr_final"):
                print(f"   ↳ {t}: {evt.get('text', '')}  ✅")
            elif t == "ready":
                print("   ↳ ready (ASR 就绪)")
            elif t == "eos":
                print("   ↳ eos")
            elif t == "error":
                print(f"   ↳ error: {evt.get('message', '')}  ❌")
            elif t == "pong":
                pass  # 安静处理
            else:
                preview = {k: (v if k != "data" else f"<{len(v)}B base64>") for k, v in evt.items()}
                print(f"   ↳ {t}: {preview}")
    except websockets.ConnectionClosed as exc:
        print(f"[mock-furniture] connection closed: {exc.code} {exc.reason}")
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


async def stream_with_vad(
    ws,
    pcm: bytes,
    wakeword: WakeWord,
    vad: VadSegmenter,
    realtime: bool,
    lookback_ms: int = 300,
) -> int:
    """按 30ms 帧扫整段 pcm；用 VAD 切句，每段独立 start / [PCM…] / end。

    ``lookback_ms``：滚动保留最近这么多毫秒的帧。VAD 触发 ``start`` 时，
    把这段"段前音频"一起送到 ASR，避免句首爆破音/擦音被吞（典型表现是
    "把客厅的灯打开" 被识别成 "客厅的灯打开"）。

    返回：触发的 ``end`` 次数（= 期望产生的 asr_final 数）。
    """
    from collections import deque

    n_frames = len(pcm) // VAD_FRAME_BYTES
    print(f"[mock-furniture] virtual mic stream: {n_frames} frames × {VAD_FRAME_MS}ms "
          f"= {n_frames * VAD_FRAME_MS} ms")

    end_count = 0
    seg_idx = 0
    seg_buf = bytearray()  # 当前段累积的 PCM，攒到 600ms 一发

    # 段前 lookback 环形缓冲：保存最近 N 帧，start 时一次性倒进段缓冲
    lookback_frames = max(0, lookback_ms // VAD_FRAME_MS)
    pre_buf: "deque[bytes]" = deque(maxlen=lookback_frames)

    async def flush_segment_buf() -> None:
        nonlocal seg_buf
        if seg_buf:
            await ws.send(bytes(seg_buf))
            seg_buf = bytearray()

    for i in range(n_frames):
        frame = pcm[i * VAD_FRAME_BYTES : (i + 1) * VAD_FRAME_BYTES]

        # 1) KWS（M2 默认 AlwaysOn，永远 active）
        wakeword.feed(frame)
        if not wakeword.is_active_window():
            if realtime:
                await asyncio.sleep(VAD_FRAME_MS / 1000.0)
            continue

        # 2) VAD 切句
        evt = vad.feed(frame)
        if evt == "start":
            seg_idx += 1
            print(f"[mock-furniture] ▶ VAD: segment #{seg_idx} START "
                  f"(+{len(pre_buf)*VAD_FRAME_MS}ms lookback)")
            await send_start(ws)
            # 把 lookback 里的"段前静音/爆破音前摇"一起送进段缓冲
            seg_buf = bytearray()
            for pf in pre_buf:
                seg_buf.extend(pf)
            pre_buf.clear()
        elif evt == "end":
            await flush_segment_buf()
            await send_end(ws)
            end_count += 1
            print(f"[mock-furniture] ■ VAD: segment #{seg_idx} END (waiting asr_final…)")

        # 3) 处于说话状态时累积音频，攒够 ~600ms 推一次
        if vad.speaking:
            seg_buf.extend(frame)
            if len(seg_buf) >= UPLINK_CHUNK_BYTES:
                await flush_segment_buf()
        else:
            # 非说话态，持续填充 lookback（供下一次 start 用）
            pre_buf.append(frame)

        if realtime:
            await asyncio.sleep(VAD_FRAME_MS / 1000.0)

    # 流末仍在说话 → 强制收尾
    if vad.speaking:
        await flush_segment_buf()
        await send_end(ws)
        end_count += 1
        print("[mock-furniture] ■ stream tail end (forced)")

    return end_count


async def stream_ptt(ws, pcm: bytes, realtime: bool) -> int:
    """PTT 模式：整段一次性 start → PCM → end。"""
    print(f"[mock-furniture] PTT: pushing {len(pcm)} bytes "
          f"({len(pcm) // (SAMPLE_RATE * SAMPLE_WIDTH // 1000)} ms) as one segment")
    await send_start(ws)
    sent = 0
    while sent < len(pcm):
        chunk = pcm[sent : sent + UPLINK_CHUNK_BYTES]
        sent += len(chunk)
        await ws.send(chunk)
        if realtime:
            await asyncio.sleep(UPLINK_CHUNK_MS / 1000.0)
    await send_end(ws)
    return 1


# ---------------------------------------------------------------------------
# 主流程
# ---------------------------------------------------------------------------


def build_url(server: str, device_id: Optional[str], token: Optional[str]) -> str:
    """如果用户给的是 server 形态（含 /v1/voice），自动拼鉴权参数。"""
    if "?" in server or not device_id:
        return server
    sep = "&" if "?" in server else "?"
    qs = f"device_id={device_id}"
    if token:
        qs += f"&token={token}"
    return f"{server}{sep}{qs}"


async def main_async(args: argparse.Namespace) -> int:
    wavs = [Path(w) for w in args.wav]
    for w in wavs:
        if not w.exists():
            print(f"[mock-furniture] wav not found: {w}")
            return 2

    print(f"[mock-furniture] loading {len(wavs)} wav file(s)…")
    pcm = build_virtual_mic_stream(wavs, gap_ms=args.gap_ms, lead_ms=args.lead_ms)
    duration_ms = len(pcm) // (SAMPLE_RATE * SAMPLE_WIDTH // 1000)
    print(f"[mock-furniture] virtual mic stream: {len(pcm)} bytes ≈ {duration_ms} ms")

    url = build_url(args.server, args.device_id, args.token)
    print(f"[mock-furniture] connecting {url}")
    try:
        ws = await websockets.connect(url, max_size=None, open_timeout=10)
    except Exception as e:  # noqa: BLE001
        print(f"[mock-furniture] connect failed: {e!r}")
        return 3
    print(f"[mock-furniture] connected (mode={args.mode})")

    stop = asyncio.Event()
    recv_task = asyncio.create_task(receiver(ws, stop))

    expected_finals = 0
    try:
        if args.mode == "vad":
            wakeword = AlwaysOnWakeWord()
            vad = VadSegmenter(
                aggressiveness=args.vad_level,
                silence_ms=args.silence_ms,
                min_speech_ms=args.min_speech_ms,
            )
            expected_finals = await stream_with_vad(
                ws, pcm, wakeword, vad, args.realtime, lookback_ms=args.lookback_ms
            )
        else:  # ptt
            expected_finals = await stream_ptt(ws, pcm, args.realtime)

        print(f"[mock-furniture] all PCM sent. expected asr_final count = {expected_finals}")
        # 给 server / ASR 时间产出最后一个 final
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
        description="Mock 家具端：wav 模拟麦克风 + KWS/VAD 自动断句 + WS 上行到 server",
        formatter_class=argparse.RawDescriptionHelpFormatter,
    )
    # 连接
    p.add_argument(
        "--server",
        default="ws://127.0.0.1:8080/v1/voice",
        help="目标 WS 地址。经 server: ws://host:8080/v1/voice；直连 ASR: ws://host:9100/v1/asr/stream",
    )
    p.add_argument("--device-id", default="dev1", help="设备 ID（仅当 server 路径需要时拼到 URL）")
    p.add_argument("--token", default="t1", help="设备 token（仅 server 透传时使用）")
    # 音源
    p.add_argument("--wav", nargs="+", required=True, help="一个或多个 wav 文件，将拼成连续麦克风流")
    p.add_argument("--gap-ms", type=int, default=1200, help="多 wav 之间的静音长度（ms），默认 1200")
    p.add_argument("--lead-ms", type=int, default=500, help="流开头的静音长度（ms），默认 500")
    # 模式
    p.add_argument("--mode", choices=["vad", "ptt"], default="vad",
                   help="vad=自动断句（默认，主流智能音箱模式）；ptt=按住说话整段送（兼容老脚本）")
    # VAD 参数
    p.add_argument("--vad-level", type=int, default=2, choices=[0, 1, 2, 3],
                   help="webrtcvad 灵敏度，0 最宽松 / 3 最严格，默认 2")
    p.add_argument("--silence-ms", type=int, default=800,
                   help="持续静音多少毫秒判定一句话结束，默认 800")
    p.add_argument("--min-speech-ms", type=int, default=90,
                   help="持续语音多少毫秒判定一句话开始，默认 90（给句首爆破音留余量）")
    p.add_argument("--lookback-ms", type=int, default=300,
                   help="start 时把前这么多毫秒的音频也送给 ASR，避免句首被吞，默认 300")
    # 节奏 / 等待
    p.add_argument("--realtime", action="store_true",
                   help="按 30ms 帧实时节奏发送（更真实但更慢）；不加 = 尽快灌完")
    p.add_argument("--hold", type=float, default=15.0,
                   help="发完后等服务端响应的最大秒数，默认 15")
    return p.parse_args()


if __name__ == "__main__":
    try:
        rc = asyncio.run(main_async(parse_args()))
    except KeyboardInterrupt:
        print("\n[mock-furniture] interrupted")
        rc = 130
    sys.exit(rc)
