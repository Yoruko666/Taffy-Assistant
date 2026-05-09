"""本地 ASR 服务（FunASR paraformer-zh-streaming + fsmn-vad）。

接口：
    GET  /v1/health              健康检查
    POST /v1/asr/transcribe      multipart 上传整段音频 -> {text, duration_ms}（兼容/调试用）
    WS   /v1/asr/stream          流式语音识别（**主接口**，供 Go Server 透传）

流式协议（WebSocket）：
    客户端 -> 服务端：
        1) 首帧 JSON 文本：{"type":"start","sample_rate":16000,"format":"pcm_s16le","channels":1}
        2) 后续二进制帧：16-bit mono PCM 原始字节，建议每 ~600ms 发一包（9600 帧 / 19200 字节）
        3) 结束 JSON 文本：{"type":"end"}
    服务端 -> 客户端：
        {"type":"ready"}                      ← 模型就绪、可以开始送音频
        {"type":"partial","text":"...部分..."} ← 中间结果（会反复修正）
        {"type":"final","text":"...最终..."}    ← 一段话识别完成（VAD 端点 / end）
        {"type":"error","message":"..."}      ← 错误信息（连接随后关闭）

环境变量：
    ASR_MODEL_DIR        ASR 流式模型目录（默认 ./models/paraformer-zh-streaming）
    ASR_VAD_MODEL_DIR    VAD 模型目录（默认 ./models/fsmn-vad）
    ASR_OFFLINE_MODEL_DIR  非流式模型目录（可选，仅 HTTP 接口用，默认与 ASR_MODEL_DIR 相同）
    ASR_DEVICE           cpu / cuda（默认 cpu）
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
DEVICE: str = os.environ.get("ASR_DEVICE", "cpu")

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
    # VAD 模型可选：存在则启用端点检测，缺失则降级为"客户端显式 end"
    if VAD_MODEL_DIR.exists():
        kwargs["vad_model"] = str(VAD_MODEL_DIR)
        kwargs["vad_kwargs"] = {"max_single_segment_time": 30000}
    else:
        logger.warning(f"VAD 模型缺失：{VAD_MODEL_DIR}，将仅依赖客户端 end 信号断句")

    logger.info(
        f"加载 ASR 流式模型：{STREAM_MODEL_DIR} "
        f"(vad={'on' if VAD_MODEL_DIR.exists() else 'off'}, device={DEVICE})"
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


app = FastAPI(title="SHVA ASR Server", version="0.2.0")


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
    """流式 ASR WebSocket 端点。详细协议见文件顶部 docstring。"""
    await ws.accept()
    logger.info(f"ws connected: client={ws.client}")

    # 1) 读取首帧 start 配置（容错：未发也按默认 16k mono PCM 处理）
    sample_rate = SAMPLE_RATE
    try:
        first = await asyncio.wait_for(ws.receive(), timeout=10.0)
    except asyncio.TimeoutError:
        await _ws_send_error(ws, "timeout_waiting_start")
        await ws.close()
        return
    except WebSocketDisconnect:
        return

    if first.get("type") == "websocket.disconnect":
        return
    if "text" in first and first["text"]:
        try:
            cfg = json.loads(first["text"])
            if isinstance(cfg, dict) and cfg.get("type") == "start":
                sample_rate = int(cfg.get("sample_rate", SAMPLE_RATE))
                if sample_rate != SAMPLE_RATE:
                    await _ws_send_error(
                        ws, f"unsupported_sample_rate={sample_rate}, expect {SAMPLE_RATE}"
                    )
                    await ws.close()
                    return
        except json.JSONDecodeError:
            await _ws_send_error(ws, "invalid_start_json")
            await ws.close()
            return
    elif "bytes" in first and first["bytes"]:
        # 客户端没发 start，直接发的二进制：放回缓冲区当作音频处理
        # 简化处理：把这帧也直接走识别管道
        pass

    # 2) 加载流式模型
    try:
        model = get_stream_model()
    except Exception as e:  # noqa: BLE001
        await _ws_send_error(ws, f"model_unavailable: {e}")
        await ws.close()
        return

    await ws.send_text(json.dumps({"type": "ready"}, ensure_ascii=False))

    # 3) 主循环：聚合二进制帧到 CHUNK_STRIDE_BYTES 后送入流式模型
    cache: Dict[str, Any] = {}
    pcm_buffer = bytearray()
    accumulated_text = ""  # 当前一句累计文本（VAD/end 后清空）
    total_audio_ms = 0
    t_start = time.time()
    loop = asyncio.get_running_loop()

    # 如果首帧就是音频，先入缓冲
    if "bytes" in first and first["bytes"]:
        pcm_buffer.extend(first["bytes"])

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
                if ptype == "end":
                    # 清空缓冲区里剩余字节并标记 final
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
                    break
                elif ptype == "ping":
                    await ws.send_text(json.dumps({"type": "pong"}, ensure_ascii=False))
                else:
                    # 忽略未知控制帧
                    pass
                continue

            # ---- 二进制音频帧 ----
            if "bytes" in msg and msg["bytes"]:
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
            f"ws closed: audio_ms={total_audio_ms} cost={time.time()-t_start:.2f}s "
            f"text={accumulated_text[:40]!r}"
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
