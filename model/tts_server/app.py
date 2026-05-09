"""本地 TTS 服务（Piper zh_CN-huayan-medium）。

接口：
    GET  /v1/health             健康检查
    POST /v1/tts/synthesize     {text, voice?, format?} -> audio/wav
"""

from __future__ import annotations

import io
import logging
import os
import time
import wave
from pathlib import Path
from typing import Dict, Optional

from fastapi import FastAPI, HTTPException
from fastapi.responses import JSONResponse, Response
from pydantic import BaseModel, Field


BASE_DIR: Path = Path(__file__).resolve().parent

DEFAULT_VOICE = "zh_CN-huayan-medium"

# 声线名 -> .onnx 文件路径
VOICE_REGISTRY: Dict[str, Path] = {
    "zh_CN-huayan-medium": BASE_DIR
    / "models" / "zh" / "zh_CN" / "huayan" / "medium" / "zh_CN-huayan-medium.onnx",
}

DEFAULT_VOICE = os.environ.get("TTS_DEFAULT_VOICE", DEFAULT_VOICE)

logging.basicConfig(
    level=logging.INFO,
    format="[tts_server] %(asctime)s %(levelname)s %(message)s",
)
logger = logging.getLogger("tts_server")


# 已加载的 PiperVoice 实例缓存
_voice_cache: Dict[str, "PiperVoice"] = {}  # type: ignore[name-defined]


def get_voice(voice: str):
    """懒加载指定声线的 Piper 模型。"""
    if voice in _voice_cache:
        return _voice_cache[voice]

    if voice not in VOICE_REGISTRY:
        raise HTTPException(
            status_code=400,
            detail=f"未知 voice：{voice}，可选：{list(VOICE_REGISTRY.keys())}",
        )

    model_path = VOICE_REGISTRY[voice]
    if not model_path.exists():
        raise HTTPException(
            status_code=503,
            detail=(
                f"声线模型未就绪：{model_path}，请先运行 model/download_model.py --only tts"
            ),
        )

    try:
        from piper.voice import PiperVoice  # type: ignore
    except ImportError as e:
        raise HTTPException(
            status_code=503,
            detail="未安装 piper-tts，请执行：pip install piper-tts",
        ) from e

    logger.info(f"加载 TTS 声线：{voice} <- {model_path}")
    t0 = time.time()
    pv = PiperVoice.load(str(model_path))
    logger.info(f"声线加载完成，耗时 {time.time() - t0:.2f}s")
    _voice_cache[voice] = pv
    return pv


app = FastAPI(title="SHVA TTS Server", version="0.1.0")


class SynthesizeReq(BaseModel):
    text: str = Field(..., description="要合成的文本", min_length=1, max_length=2000)
    voice: Optional[str] = Field(default=None, description="声线名，缺省用默认声线")
    format: Optional[str] = Field(default="wav", description="输出格式：wav（默认）")


@app.on_event("startup")
def on_startup() -> None:
    # 预加载默认声线
    try:
        get_voice(DEFAULT_VOICE)
    except Exception as e:  # noqa: BLE001
        logger.warning(f"启动预加载失败（将在首个请求时重试）：{e!r}")


@app.get("/v1/health")
def health() -> dict:
    return {
        "status": "ok" if _voice_cache else "loading",
        "default_voice": DEFAULT_VOICE,
        "voices": list(VOICE_REGISTRY.keys()),
        "loaded_voices": list(_voice_cache.keys()),
    }


def _synthesize_to_wav_bytes(pv, text: str) -> bytes:
    """调用 Piper 合成并打包 WAV 字节，兼容多版本 piper-tts API。"""
    # 新版（>=1.3）：synthesize(text) -> Iterable[AudioChunk]
    try:
        chunks = list(pv.synthesize(text))
        if chunks and hasattr(chunks[0], "audio_int16_bytes"):
            first = chunks[0]
            sample_rate = int(getattr(first, "sample_rate", 22050))
            sample_width = int(getattr(first, "sample_width", 2))
            channels = int(getattr(first, "sample_channels", 1))

            buf = io.BytesIO()
            with wave.open(buf, "wb") as wav_file:
                wav_file.setnchannels(channels)
                wav_file.setsampwidth(sample_width)
                wav_file.setframerate(sample_rate)
                for ch in chunks:
                    wav_file.writeframes(ch.audio_int16_bytes)
            return buf.getvalue()
    except TypeError:
        # 老版 synthesize 需要 wav_file 参数，走下方兜底
        pass

    # 旧版：synthesize_stream_raw(text) -> Iterable[bytes]
    if hasattr(pv, "synthesize_stream_raw"):
        sample_rate = int(getattr(pv.config, "sample_rate", 22050))
        buf = io.BytesIO()
        with wave.open(buf, "wb") as wav_file:
            wav_file.setnchannels(1)
            wav_file.setsampwidth(2)
            wav_file.setframerate(sample_rate)
            for raw in pv.synthesize_stream_raw(text):
                wav_file.writeframes(raw)
        return buf.getvalue()

    # 更旧版：synthesize(text, wav_file) 直接写入
    buf = io.BytesIO()
    with wave.open(buf, "wb") as wav_file:
        wav_file.setnchannels(1)
        wav_file.setsampwidth(2)
        wav_file.setframerate(int(getattr(pv.config, "sample_rate", 22050)))
        pv.synthesize(text, wav_file)
    return buf.getvalue()


@app.post("/v1/tts/synthesize")
def synthesize(req: SynthesizeReq) -> Response:
    """文本转语音，返回二进制 audio/wav。"""
    text = (req.text or "").strip()
    if not text:
        raise HTTPException(status_code=400, detail="text 不能为空")

    voice_name = req.voice or DEFAULT_VOICE
    fmt = (req.format or "wav").lower()
    if fmt != "wav":
        raise HTTPException(status_code=400, detail=f"暂不支持的输出格式：{fmt}")

    pv = get_voice(voice_name)

    t0 = time.time()
    try:
        audio = _synthesize_to_wav_bytes(pv, text)
    except Exception as e:  # noqa: BLE001
        logger.exception("TTS 合成失败")
        return JSONResponse(
            status_code=500,
            content={"error": "synthesize_failed", "message": str(e)},
        )

    logger.info(
        f"synthesize ok: voice={voice_name} text_len={len(text)} "
        f"bytes={len(audio)} cost={time.time()-t0:.2f}s"
    )
    return Response(
        content=audio,
        media_type="audio/wav",
        headers={
            "Content-Disposition": f'inline; filename="tts_{int(time.time())}.wav"',
            "X-TTS-Voice": voice_name,
        },
    )


if __name__ == "__main__":
    import uvicorn

    uvicorn.run(
        "tts_server.app:app",
        host="0.0.0.0",
        port=int(os.environ.get("TTS_PORT", 9200)),
        reload=False,
    )
