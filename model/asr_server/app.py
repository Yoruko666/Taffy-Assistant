"""本地 ASR 服务（FunASR paraformer-zh-streaming + fsmn-vad）。

接口：
    GET  /v1/health              健康检查
    POST /v1/asr/transcribe      multipart 上传整段音频 -> {text, duration_ms}（兼容/调试用）
    WS   /v1/asr/stream          流式语音识别（**主接口**，供 Go Server 透传）

流式协议（WebSocket，**支持单连接多段对话**）：
    客户端 -> 服务端（同一连接内可重复 N 次）：
        1) 段首 JSON 文本：{"type":"start","sample_rate":16000,"format":"pcm_s16le","channels":1}
        2) 二进制帧：16-bit mono PCM 原始字节，建议每 ~600ms 发一包（9600 采样 / 19200 字节）
        3) 段尾 JSON 文本：{"type":"end"}   ← 触发一次 final+eos，**连接不关**，可继续下一段
        4) 心跳：{"type":"ping"}             ← 服务端回 {"type":"pong"}
        5) 关闭：由客户端主动关 WS
    服务端 -> 客户端：
        {"type":"ready"}                      ← 模型就绪，可以开始送音频（整个连接只发一次）
        {"type":"partial","text":"..."}       ← 段内中间结果（会反复修正，含累计文本）
        {"type":"final","text":"..."}         ← 本段识别完成（end 触发）
        {"type":"eos"}                        ← 本段已结束，下一段可 start
        {"type":"error","message":"..."}      ← 错误信息
    语义要点：
        - ASR cache 随每段独立：每次 end/start 之间重置，避免相邻两句相互污染。
        - 一条 WS 可承载多段语音（主流智能音箱模式：WS 长连接 + 端侧 VAD 切句）。

环境变量：
    ASR_MODEL_DIR        ASR 流式模型目录（默认 ./models/paraformer-zh-streaming）
    ASR_VAD_MODEL_DIR    VAD 模型目录（默认 ./models/fsmn-vad）
    ASR_OFFLINE_MODEL_DIR  非流式模型目录（可选，仅 HTTP 接口用，默认与 ASR_MODEL_DIR 相同）
    ASR_DEVICE           cpu / cuda（默认 cuda）
    ASR_PORT             监听端口（默认 9100）
"""

from __future__ import annotations

import asyncio
import json
import logging
import os
import time
from pathlib import Path
from typing import Any, Dict, List, Optional

from fastapi import FastAPI, File, HTTPException, UploadFile, WebSocket, WebSocketDisconnect
from fastapi.responses import JSONResponse


BASE_DIR: Path = Path(__file__).resolve().parent
DEFAULT_STREAM_MODEL_DIR: Path = BASE_DIR / "models" / "paraformer-zh-streaming"
DEFAULT_VAD_MODEL_DIR: Path = BASE_DIR / "models" / "fsmn-vad"
DEFAULT_OFFLINE_MODEL_DIR: Path = BASE_DIR / "models" / "paraformer-zh"

# 支持通过环境变量覆盖模型目录与运行设备
STREAM_MODEL_DIR: Path = Path(
    os.environ.get("ASR_MODEL_DIR", str(DEFAULT_STREAM_MODEL_DIR))
)
VAD_MODEL_DIR: Path = Path(
    os.environ.get("ASR_VAD_MODEL_DIR", str(DEFAULT_VAD_MODEL_DIR))
)
OFFLINE_MODEL_DIR: Path = Path(
    os.environ.get(
        "ASR_OFFLINE_MODEL_DIR",
        str(DEFAULT_OFFLINE_MODEL_DIR if DEFAULT_OFFLINE_MODEL_DIR.exists() else STREAM_MODEL_DIR),
    )
)
DEVICE: str = os.environ.get("ASR_DEVICE", "cuda")

# 流式分块参数（FunASR 标准配置）
# chunk_size = [0, 10, 5] 表示：左 0 编码块、当前 10 编码块（≈600ms）、右 5 编码块（≈300ms 前瞻）
# encoder_chunk_look_back / decoder_chunk_look_back：跨块上下文回看深度，越大越准但延迟略高
CHUNK_SIZE: List[int] = [0, 10, 5]
ENCODER_LOOK_BACK: int = 4
DECODER_LOOK_BACK: int = 1
# 单帧 PCM 时长（ms）≈ chunk_size[1] * 60ms = 600ms
CHUNK_STRIDE_MS: int = CHUNK_SIZE[1] * 60
SAMPLE_RATE: int = 16000
# 16kHz × 16-bit mono：1 ms = 32 字节；600ms = 19200 字节
CHUNK_STRIDE_BYTES: int = CHUNK_STRIDE_MS * SAMPLE_RATE * 2 // 1000

logging.basicConfig(
    level=logging.INFO,
    format="[asr_server] %(asctime)s %(levelname)s %(message)s",
)
logger = logging.getLogger("asr_server")


# 进程级单例，避免重复加载模型
_stream_model: Any = None
_offline_model: Any = None


def get_stream_model() -> Any:
    """懒加载流式 FunASR AutoModel（paraformer-zh-streaming + fsmn-vad）。"""
    global _stream_model
    if _stream_model is not None:
        return _stream_model

    try:
        from funasr import AutoModel  # type: ignore
    except ImportError as e:
        raise RuntimeError("未安装 funasr，请先执行：pip install funasr") from e

    if not STREAM_MODEL_DIR.exists():
        raise RuntimeError(
            f"ASR 流式模型目录不存在：{STREAM_MODEL_DIR}，"
            f"请先运行 model/download_model.py --only asr"
        )

    kwargs: Dict[str, Any] = {
        "model": str(STREAM_MODEL_DIR),
        "device": DEVICE,
        "disable_update": True,
    }
    use_vad = os.environ.get("ASR_USE_VAD", "0").lower() in ("1", "true", "yes")
    if use_vad and VAD_MODEL_DIR.exists():
        kwargs["vad_model"] = str(VAD_MODEL_DIR)
        kwargs["vad_kwargs"] = {"max_single_segment_time": 30000}
    elif use_vad:
        logger.warning(f"ASR_USE_VAD=1 但 VAD 模型缺失：{VAD_MODEL_DIR}，将仅依赖客户端 end 信号断句")
    else:
        logger.info("VAD 已禁用（依赖客户端 end 信号断句）；如需开启请设 ASR_USE_VAD=1")

    logger.info(
        f"加载 ASR 流式模型：{STREAM_MODEL_DIR} "
        f"(vad={'on' if 'vad_model' in kwargs else 'off'}, device={DEVICE})"
    )
    t0 = time.time()
    _stream_model = AutoModel(**kwargs)
    logger.info(f"ASR 流式模型加载完成，耗时 {time.time() - t0:.1f}s")
    return _stream_model


def get_offline_model() -> Any:
    """懒加载非流式 FunASR AutoModel（用于 HTTP /transcribe 整段识别）。

    若没有单独的非流式模型目录，复用流式模型也能跑（精度略低于专用非流式模型）。
    """
    global _offline_model
    if _offline_model is not None:
        return _offline_model

    try:
        from funasr import AutoModel  # type: ignore
    except ImportError as e:
        raise RuntimeError("未安装 funasr，请先执行：pip install funasr") from e

    target_dir = OFFLINE_MODEL_DIR if OFFLINE_MODEL_DIR.exists() else STREAM_MODEL_DIR
    if not target_dir.exists():
        raise RuntimeError(
            f"ASR 模型目录不存在：{target_dir}，请先运行 model/download_model.py --only asr"
        )

    logger.info(f"加载 ASR 非流式模型：{target_dir} (device={DEVICE})")
    t0 = time.time()
    _offline_model = AutoModel(
        model=str(target_dir),
        device=DEVICE,
        disable_update=True,
    )
    logger.info(f"ASR 非流式模型加载完成，耗时 {time.time() - t0:.1f}s")
    return _offline_model


app = FastAPI(title="Taffy ASR Server", version="0.2.0")


@app.on_event("startup")
def on_startup() -> None:
    # 预加载流式模型，避免首个请求超时
    try:
        get_stream_model()
    except Exception as e:  # noqa: BLE001
        logger.warning(f"启动预加载失败（将在首个请求时重试）：{e!r}")


@app.get("/v1/health")
def health() -> dict:
    return {
        "status": "ok" if _stream_model is not None else "loading",
        "stream_model": str(STREAM_MODEL_DIR),
        "vad_model": str(VAD_MODEL_DIR) if VAD_MODEL_DIR.exists() else None,
        "offline_model": str(OFFLINE_MODEL_DIR),
        "device": DEVICE,
        "stream_loaded": _stream_model is not None,
        "offline_loaded": _offline_model is not None,
        "chunk_stride_ms": CHUNK_STRIDE_MS,
    }


# ---------------------------------------------------------------------------
# HTTP：整段音频识别（向后兼容 / 调试 / 冒烟测试）
# ---------------------------------------------------------------------------

_ALLOWED_EXT = {".wav", ".pcm", ".mp3", ".m4a", ".flac", ".ogg", ".webm", ".aac"}


def _save_upload_to_tmp(upload: UploadFile, raw: bytes) -> Path:
    """将上传的音频字节写入本地临时文件，返回文件路径。"""
    suffix = Path(upload.filename or "audio.wav").suffix.lower()
    if suffix not in _ALLOWED_EXT:
        logger.info(f"不常见音频扩展名 {suffix}，仍尝试识别")

    tmp_dir = BASE_DIR / ".cache"
    tmp_dir.mkdir(parents=True, exist_ok=True)
    tmp_file = tmp_dir / f"asr_{int(time.time() * 1000)}_{os.getpid()}{suffix or '.wav'}"
    tmp_file.write_bytes(raw)
    return tmp_file


def _infer_duration_ms(path: Path) -> Optional[int]:
    """尝试读取音频时长（毫秒），失败返回 None。"""
    try:
        import soundfile as sf  # type: ignore

        info = sf.info(str(path))
        return int(info.frames / info.samplerate * 1000)
    except Exception:
        return None


@app.post("/v1/asr/transcribe")
async def transcribe(audio: UploadFile = File(..., description="音频文件")) -> JSONResponse:
    """语音转写接口（**整段识别**，主要用于调试与冒烟测试）。

    生产路径请使用 WebSocket /v1/asr/stream 以获得流式低延迟体验。

    请求：multipart/form-data，字段 `audio`
    响应：200 {"text": "...", "duration_ms": 1234}
    """
    if audio is None:
        raise HTTPException(status_code=400, detail="缺少字段 audio")

    raw = await audio.read()
    if not raw:
        raise HTTPException(status_code=400, detail="音频内容为空")

    tmp_file = _save_upload_to_tmp(audio, raw)
    try:
        model = get_offline_model()
    except Exception as e:  # noqa: BLE001
        return JSONResponse(
            status_code=503,
            content={"error": "model_unavailable", "message": str(e)},
        )

    t0 = time.time()
    try:
        result = model.generate(input=str(tmp_file))
    except Exception as e:  # noqa: BLE001
        logger.exception("ASR 推理失败")
        return JSONResponse(
            status_code=500,
            content={"error": "infer_failed", "message": str(e)},
        )
    finally:
        try:
            tmp_file.unlink(missing_ok=True)
        except Exception:
            pass

    text = ""
    if isinstance(result, list) and result:
        text = (result[0] or {}).get("text", "") or ""

    duration_ms = _infer_duration_ms(tmp_file) or int((time.time() - t0) * 1000)
    logger.info(
        f"transcribe ok: len={len(raw)}B cost={time.time()-t0:.2f}s text={text[:40]!r}"
    )

    return JSONResponse(
        status_code=200,
        content={
            "text": text,
            "duration_ms": duration_ms,
        },
    )


# ---------------------------------------------------------------------------
# WebSocket：流式识别（主接口）
# ---------------------------------------------------------------------------

def _pcm_bytes_to_float32(pcm_bytes: bytes) -> "Any":
    """16-bit little-endian PCM -> float32 [-1, 1] numpy 数组。"""
    import numpy as np  # 局部 import，避免顶部强依赖

    if len(pcm_bytes) == 0:
        return np.zeros(0, dtype="float32")
    arr = np.frombuffer(pcm_bytes, dtype="<i2").astype("float32") / 32768.0
    return arr


def _run_stream_chunk(
    model: Any,
    pcm_chunk: "Any",
    cache: Dict[str, Any],
    is_final: bool,
) -> str:
    """在线程池里跑一次流式 generate 调用，返回该块识别出的文本片段。"""
    res = model.generate(
        input=pcm_chunk,
        cache=cache,
        is_final=is_final,
        chunk_size=CHUNK_SIZE,
        encoder_chunk_look_back=ENCODER_LOOK_BACK,
        decoder_chunk_look_back=DECODER_LOOK_BACK,
    )
    if isinstance(res, list) and res:
        return (res[0] or {}).get("text", "") or ""
    return ""


@app.websocket("/v1/asr/stream")
async def asr_stream(ws: WebSocket) -> None:
    """流式 ASR WebSocket 端点。详细协议见文件顶部 docstring。

    **单 WS 多段对话**：
        连接后先发一次 ready；随后循环 "start -> [PCM...] -> end"，
        每个 end 触发一次 final + eos，cache 清空，连接保持，等待下一段 start。
        客户端主动关 WS 才结束。
    """
    await ws.accept()
    logger.info(f"ws connected: client={ws.client}")

    # 1) 加载模型（仅加载一次，失败即关）
    try:
        model = get_stream_model()
    except Exception as e:  # noqa: BLE001
        await _ws_send_error(ws, f"model_unavailable: {e}")
        await ws.close()
        return

    await ws.send_text(json.dumps({"type": "ready"}, ensure_ascii=False))

    loop = asyncio.get_running_loop()

    # 段级状态（每个 start~end 之间独立）
    in_segment = False
    cache: Dict[str, Any] = {}
    pcm_buffer = bytearray()
    accumulated_text = ""

    # 会话级计数
    seg_count = 0
    t_conn = time.time()
    total_audio_ms = 0

    def reset_segment() -> None:
        """一段话结束后清空段级状态，等待下一个 start。"""
        nonlocal in_segment, cache, pcm_buffer, accumulated_text
        in_segment = False
        cache = {}
        pcm_buffer = bytearray()
        accumulated_text = ""

    try:
        while True:
            try:
                msg = await ws.receive()
            except WebSocketDisconnect:
                logger.info("ws disconnected by client")
                break

            mtype = msg.get("type")
            if mtype == "websocket.disconnect":
                break

            # ---- 文本控制帧 ----
            if "text" in msg and msg["text"]:
                try:
                    payload = json.loads(msg["text"])
                except json.JSONDecodeError:
                    await _ws_send_error(ws, "invalid_control_json")
                    continue

                ptype = payload.get("type")

                if ptype == "start":
                    # 新一段开始：校验采样率、重置段状态
                    sr = int(payload.get("sample_rate", SAMPLE_RATE))
                    if sr != SAMPLE_RATE:
                        await _ws_send_error(
                            ws, f"unsupported_sample_rate={sr}, expect {SAMPLE_RATE}"
                        )
                        continue
                    reset_segment()
                    in_segment = True
                    seg_count += 1
                    logger.debug(f"segment #{seg_count} start")

                elif ptype == "end":
                    if not in_segment:
                        # 容错：未 start 就 end，忽略（某些客户端的冗余 end）
                        continue
                    # 清空缓冲剩余字节并标记 final
                    if pcm_buffer:
                        pcm = _pcm_bytes_to_float32(bytes(pcm_buffer))
                        pcm_buffer.clear()
                    else:
                        import numpy as np  # type: ignore

                        pcm = np.zeros(0, dtype="float32")
                    seg_text = await loop.run_in_executor(
                        None, _run_stream_chunk, model, pcm, cache, True
                    )
                    if seg_text:
                        accumulated_text += seg_text
                    if accumulated_text:
                        await ws.send_text(
                            json.dumps(
                                {"type": "final", "text": accumulated_text},
                                ensure_ascii=False,
                            )
                        )
                    await ws.send_text(json.dumps({"type": "eos"}, ensure_ascii=False))
                    logger.info(
                        f"segment #{seg_count} done: text={accumulated_text[:40]!r}"
                    )
                    reset_segment()  # 为下一段清零，连接保持

                elif ptype == "ping":
                    await ws.send_text(json.dumps({"type": "pong"}, ensure_ascii=False))
                else:
                    # 忽略未知控制帧
                    pass
                continue

            # ---- 二进制音频帧 ----
            if "bytes" in msg and msg["bytes"]:
                if not in_segment:
                    # 容错：未 start 就送音频，启动一个隐式段（兼容首连 PTT 模式）
                    in_segment = True
                    seg_count += 1
                    logger.debug(f"segment #{seg_count} implicit start (no start frame)")

                pcm_buffer.extend(msg["bytes"])
                # 凑齐一个 stride 就识别一次
                while len(pcm_buffer) >= CHUNK_STRIDE_BYTES:
                    chunk_bytes = bytes(pcm_buffer[:CHUNK_STRIDE_BYTES])
                    del pcm_buffer[:CHUNK_STRIDE_BYTES]
                    total_audio_ms += CHUNK_STRIDE_MS

                    pcm = _pcm_bytes_to_float32(chunk_bytes)
                    seg_text = await loop.run_in_executor(
                        None, _run_stream_chunk, model, pcm, cache, False
                    )
                    if seg_text:
                        accumulated_text += seg_text
                        await ws.send_text(
                            json.dumps(
                                {"type": "partial", "text": accumulated_text},
                                ensure_ascii=False,
                            )
                        )
                continue

    except Exception as e:  # noqa: BLE001
        logger.exception("ws stream error")
        try:
            await _ws_send_error(ws, f"internal_error: {e}")
        except Exception:
            pass
    finally:
        try:
            await ws.close()
        except Exception:
            pass
        logger.info(
            f"ws closed: segments={seg_count} audio_ms={total_audio_ms} "
            f"session_s={time.time()-t_conn:.2f}"
        )


async def _ws_send_error(ws: WebSocket, message: str) -> None:
    try:
        await ws.send_text(
            json.dumps({"type": "error", "message": message}, ensure_ascii=False)
        )
    except Exception:
        pass


if __name__ == "__main__":
    import uvicorn

    uvicorn.run(
        "asr_server.app:app",
        host="0.0.0.0",
        port=int(os.environ.get("ASR_PORT", 9100)),
        reload=False,
    )
