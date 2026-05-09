"""本地 ASR 服务（FunASR paraformer-zh）。

接口：
    GET  /v1/health              健康检查
    POST /v1/asr/transcribe      multipart 上传音频 -> {text, duration_ms}
"""

from __future__ import annotations

import logging
import os
import time
from pathlib import Path
from typing import Optional

from fastapi import FastAPI, File, HTTPException, UploadFile
from fastapi.responses import JSONResponse


BASE_DIR: Path = Path(__file__).resolve().parent
DEFAULT_MODEL_DIR: Path = BASE_DIR / "models" / "paraformer-zh"

# 支持通过环境变量覆盖模型目录与运行设备
MODEL_DIR: Path = Path(os.environ.get("ASR_MODEL_DIR", str(DEFAULT_MODEL_DIR)))
DEVICE: str = os.environ.get("ASR_DEVICE", "cpu")

logging.basicConfig(
    level=logging.INFO,
    format="[asr_server] %(asctime)s %(levelname)s %(message)s",
)
logger = logging.getLogger("asr_server")


# 进程级单例，避免重复加载模型
_asr_model = None


def get_model():
    """懒加载 FunASR AutoModel。"""
    global _asr_model
    if _asr_model is not None:
        return _asr_model

    try:
        from funasr import AutoModel  # type: ignore
    except ImportError as e:
        raise RuntimeError("未安装 funasr，请先执行：pip install funasr") from e

    if not MODEL_DIR.exists():
        raise RuntimeError(
            f"ASR 模型目录不存在：{MODEL_DIR}，请先运行 model/download_model.py --only asr"
        )

    logger.info(f"加载 ASR 模型：{MODEL_DIR} (device={DEVICE})")
    t0 = time.time()
    _asr_model = AutoModel(
        model=str(MODEL_DIR),
        device=DEVICE,
        disable_update=True,
    )
    logger.info(f"ASR 模型加载完成，耗时 {time.time() - t0:.1f}s")
    return _asr_model


app = FastAPI(title="SHVA ASR Server", version="0.1.0")


@app.on_event("startup")
def on_startup() -> None:
    # 预加载，避免首个请求超时
    try:
        get_model()
    except Exception as e:  # noqa: BLE001
        logger.warning(f"启动预加载失败（将在首个请求时重试）：{e!r}")


@app.get("/v1/health")
def health() -> dict:
    loaded = _asr_model is not None
    return {
        "status": "ok" if loaded else "loading",
        "model": str(MODEL_DIR),
        "device": DEVICE,
        "loaded": loaded,
    }


# 常见音频扩展名（非白名单强制，仅作提示）
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
    """语音转写接口。

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
        model = get_model()
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
    logger.info(f"transcribe ok: len={len(raw)}B cost={time.time()-t0:.2f}s text={text[:40]!r}")

    return JSONResponse(
        status_code=200,
        content={
            "text": text,
            "duration_ms": duration_ms,
        },
    )


if __name__ == "__main__":
    import uvicorn

    uvicorn.run(
        "asr_server.app:app",
        host="0.0.0.0",
        port=int(os.environ.get("ASR_PORT", 9100)),
        reload=False,
    )
