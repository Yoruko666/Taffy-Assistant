"""端到端冒烟测试：TTS 合成中文 -> 写盘 -> ASR 识别 -> 打印对比。

用法（确保 start_all 已启动）：
    python test_e2e.py
    python test_e2e.py --text "把客厅灯打开"
"""
from __future__ import annotations

import argparse
import sys
import time
from pathlib import Path

import requests

ASR_URL = "http://127.0.0.1:9100"
TTS_URL = "http://127.0.0.1:9200"
OUT_WAV = Path(__file__).parent / "test_e2e.wav"


def check_health(name: str, base: str) -> None:
    """健康检查，兼容 ASR(loaded) 与 TTS(loaded_voices) 两种就绪标识。"""
    try:
        r = requests.get(f"{base}/v1/health", timeout=3)
        r.raise_for_status()
        data = r.json()
    except Exception as e:  # noqa: BLE001
        print(f"[{name}] health FAIL: {e!r}")
        sys.exit(1)

    ready = bool(data.get("loaded")) or bool(data.get("loaded_voices"))
    if not ready:
        print(f"[{name}] not ready yet: {data}")
        sys.exit(1)
    print(f"[{name}] health OK -> {data}")


def call_tts(text: str) -> bytes:
    t0 = time.time()
    r = requests.post(
        f"{TTS_URL}/v1/tts/synthesize",
        json={"text": text, "format": "wav"},
        timeout=30,
    )
    r.raise_for_status()
    audio = r.content
    print(f"[TTS] ok, {len(audio)} bytes, {(time.time()-t0)*1000:.0f} ms")
    return audio


def call_asr(wav_path: Path) -> str:
    t0 = time.time()
    with wav_path.open("rb") as f:
        r = requests.post(
            f"{ASR_URL}/v1/asr/transcribe",
            files={"audio": (wav_path.name, f, "audio/wav")},
            timeout=60,
        )
    r.raise_for_status()
    data = r.json()
    print(f"[ASR] ok, {(time.time()-t0)*1000:.0f} ms -> {data}")
    return data.get("text", "")


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--text", default="把客厅的灯打开")
    args = parser.parse_args()

    print("=== 1) 健康检查 ===")
    check_health("ASR", ASR_URL)
    check_health("TTS", TTS_URL)

    print("\n=== 2) TTS 合成 ===")
    audio = call_tts(args.text)
    OUT_WAV.write_bytes(audio)
    print(f"[TTS] wav saved -> {OUT_WAV}")

    print("\n=== 3) ASR 识别 ===")
    recognized = call_asr(OUT_WAV)

    print("\n=== 4) 对比结果 ===")
    print(f"原文: {args.text}")
    print(f"识别: {recognized}")
    print("PASS" if args.text in recognized or recognized in args.text else "DIFF (人耳判断即可)")


if __name__ == "__main__":
    main()
