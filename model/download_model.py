"""下载本项目的本地语音模型到 model/ 对应目录。

    ASR：FunASR paraformer-zh  -> asr_server/models/paraformer-zh/
    TTS：Piper huayan-medium    -> tts_server/models/zh/zh_CN/huayan/medium/

LLM 走云端 API，无需下载。默认使用 hf-mirror 镜像，失败回退 HuggingFace 官方。

用法：
    python download_model.py                 # 全部下载
    python download_model.py --only asr      # 只下 ASR
    python download_model.py --only tts      # 只下 TTS
    python download_model.py --official      # 强制走 HuggingFace 官方
"""

from __future__ import annotations

import argparse
import os
import sys
import time
from pathlib import Path
from typing import Callable, Dict, List, Optional


BASE_DIR: Path = Path(__file__).resolve().parent
ASR_DIR: Path = BASE_DIR / "asr_server" / "models"
TTS_DIR: Path = BASE_DIR / "tts_server" / "models"


# ASR 整仓下载
ASR_REPO: str = "funasr/paraformer-zh"
ASR_LOCAL_SUBDIR: str = "paraformer-zh"

# TTS 仅拉取中文 huayan-medium 两个文件
TTS_REPO: str = "rhasspy/piper-voices"
TTS_FILES: List[str] = [
    "zh/zh_CN/huayan/medium/zh_CN-huayan-medium.onnx",
    "zh/zh_CN/huayan/medium/zh_CN-huayan-medium.onnx.json",
]


HF_OFFICIAL_ENDPOINT: str = "https://huggingface.co"
HF_MIRROR_ENDPOINT: str = "https://hf-mirror.com"


def log(msg: str) -> None:
    print(f"[download_model] {msg}", flush=True)


def ensure_dir(path: Path) -> None:
    path.mkdir(parents=True, exist_ok=True)


def human_size(num_bytes: int) -> str:
    units = ["B", "KB", "MB", "GB", "TB"]
    size = float(num_bytes)
    for unit in units:
        if size < 1024.0:
            return f"{size:.2f} {unit}"
        size /= 1024.0
    return f"{size:.2f} PB"


def use_endpoint(endpoint: str) -> None:
    os.environ["HF_ENDPOINT"] = endpoint
    log(f"使用 HF 端点：{endpoint}")


def _download_single_file(
    repo_id: str,
    filename: str,
    target_dir: Path,
) -> Optional[Path]:
    """下载仓库中的单个文件到 target_dir，支持断点续传。"""
    try:
        from huggingface_hub import hf_hub_download
    except ImportError:
        log("未检测到 huggingface_hub，请先运行 setup_gguf.py 或：pip install huggingface_hub")
        return None

    ensure_dir(target_dir)
    log(f"下载文件：{repo_id} :: {filename}  ->  {target_dir}")
    start = time.time()
    try:
        local_path = hf_hub_download(
            repo_id=repo_id,
            filename=filename,
            local_dir=str(target_dir),
            local_dir_use_symlinks=False,
            resume_download=True,
        )
    except Exception as e:  # noqa: BLE001
        log(f"下载失败：{e!r}")
        return None

    final = Path(local_path)
    if final.exists():
        log(
            f"  OK  {final.name}  "
            f"{human_size(final.stat().st_size)}  耗时 {time.time()-start:.1f}s"
        )
        return final
    return None


def _download_snapshot(
    repo_id: str,
    target_dir: Path,
) -> Optional[Path]:
    """整仓快照下载。"""
    try:
        from huggingface_hub import snapshot_download
    except ImportError:
        log("未检测到 huggingface_hub，请先运行 setup_gguf.py 或：pip install huggingface_hub")
        return None

    ensure_dir(target_dir)
    log(f"下载整仓：{repo_id}  ->  {target_dir}")
    start = time.time()
    try:
        local_path = snapshot_download(
            repo_id=repo_id,
            local_dir=str(target_dir),
            local_dir_use_symlinks=False,
            resume_download=True,
        )
    except Exception as e:  # noqa: BLE001
        log(f"下载失败：{e!r}")
        return None

    log(f"  OK  整仓快照完成  耗时 {time.time()-start:.1f}s")
    return Path(local_path)


def download_asr() -> bool:
    target_dir = ASR_DIR / ASR_LOCAL_SUBDIR
    if target_dir.exists() and any(target_dir.iterdir()):
        log(f"[ASR] 目录已存在且非空，跳过：{target_dir}")
        return True
    result = _download_snapshot(ASR_REPO, target_dir)
    return result is not None


def download_tts() -> bool:
    ok = True
    for f in TTS_FILES:
        result = _download_single_file(TTS_REPO, f, TTS_DIR)
        if result is None:
            ok = False
    return ok


TASKS: Dict[str, Callable[[], bool]] = {
    "asr": download_asr,
    "tts": download_tts,
}


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="下载本项目所需的 ASR / TTS 本地模型（LLM 走云端 API，无需下载）"
    )
    parser.add_argument(
        "--only",
        choices=["asr", "tts"],
        help="只下载某一类模型（默认全部）",
    )
    parser.add_argument(
        "--mirror",
        action="store_true",
        help="强制使用国内镜像 hf-mirror.com（默认已优先镜像）",
    )
    parser.add_argument(
        "--official",
        action="store_true",
        help="强制使用 HuggingFace 官方端点",
    )
    return parser.parse_args()


def main() -> int:
    args = parse_args()

    if args.official:
        endpoints = [HF_OFFICIAL_ENDPOINT]
    elif args.mirror:
        endpoints = [HF_MIRROR_ENDPOINT]
    else:
        endpoints = [HF_MIRROR_ENDPOINT, HF_OFFICIAL_ENDPOINT]

    task_names = [args.only] if args.only else ["asr", "tts"]
    log(f"准备下载：{', '.join(task_names)}")

    results: Dict[str, bool] = {}
    for name in task_names:
        log(f"========== 开始下载 [{name.upper()}] ==========")
        success = False
        for ep in endpoints:
            use_endpoint(ep)
            if TASKS[name]():
                success = True
                break
            log(f"[{name}] 当前端点失败，尝试下一个 ...")
        results[name] = success

    log("========== 下载结果汇总 ==========")
    all_ok = True
    for name, ok in results.items():
        log(f"  {name.upper():<4} : {'OK' if ok else 'FAIL'}")
        if not ok:
            all_ok = False

    if not all_ok:
        log("部分模型下载失败，可重跑本脚本（已下载的会自动跳过）")
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
