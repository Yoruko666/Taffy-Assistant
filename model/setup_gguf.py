"""下载 GGUF 本地大模型推理相关依赖到 model/gguf_pkg/ 目录（离线快照）。

仅 pip download，不安装到系统 Python。如需离线安装：
    pip install --no-index --find-links=./gguf_pkg llama-cpp-python huggingface_hub
"""

from __future__ import annotations

import shutil
import subprocess
import sys
from pathlib import Path
from typing import List


BASE_DIR: Path = Path(__file__).resolve().parent
PKG_DIR: Path = BASE_DIR / "gguf_pkg"

# 核心依赖：
#   llama-cpp-python：GGUF 推理；huggingface_hub：下载 gguf；requests/tqdm：网络与进度条
REQUIREMENTS: List[str] = [
    "llama-cpp-python",
    "huggingface_hub",
    "requests",
    "tqdm",
]

# abetlen 预编译 wheel 源（CPU 版），避免 Windows 本地编译
LLAMA_CPP_WHL_INDEX: str = "https://abetlen.github.io/llama-cpp-python/whl/cpu"

# 镜像源候选，按顺序尝试
PYPI_MIRRORS: List[str] = [
    "https://pypi.tuna.tsinghua.edu.cn/simple",
    "https://mirrors.aliyun.com/pypi/simple",
    "https://pypi.org/simple",
]


def log(msg: str) -> None:
    print(f"[setup_gguf] {msg}", flush=True)


def ensure_dir(path: Path) -> None:
    path.mkdir(parents=True, exist_ok=True)


def run_pip_download(mirror: str) -> bool:
    """使用指定镜像源执行 pip download 到 PKG_DIR，仅下载 wheel。"""
    cmd = [
        sys.executable, "-m", "pip", "download",
        "--dest", str(PKG_DIR),
        "--index-url", mirror,
        "--extra-index-url", LLAMA_CPP_WHL_INDEX,
        "--trusted-host", mirror.split("/")[2],
        "--trusted-host", "abetlen.github.io",
        "--only-binary=:all:",
        *REQUIREMENTS,
    ]
    log(f"执行命令：{' '.join(cmd)}")
    try:
        subprocess.check_call(cmd)
        return True
    except subprocess.CalledProcessError as e:
        log(f"使用镜像 {mirror} 下载失败：returncode={e.returncode}")
        return False
    except Exception as e:  # noqa: BLE001
        log(f"使用镜像 {mirror} 下载异常：{e!r}")
        return False


def main() -> int:
    log(f"目标目录：{PKG_DIR}")
    ensure_dir(PKG_DIR)

    # 保留 models/ 子目录，清理其它旧内容避免版本混淆
    for item in PKG_DIR.iterdir():
        if item.name == "models":
            continue
        try:
            if item.is_dir():
                shutil.rmtree(item)
            else:
                item.unlink()
        except Exception as e:  # noqa: BLE001
            log(f"清理 {item} 失败（忽略）：{e!r}")

    try:
        subprocess.check_call(
            [sys.executable, "-m", "pip", "install", "--upgrade", "pip"]
        )
    except Exception as e:  # noqa: BLE001
        log(f"升级 pip 失败（不影响下载，继续）：{e!r}")

    for mirror in PYPI_MIRRORS:
        log(f"尝试镜像：{mirror}")
        if run_pip_download(mirror):
            log("依赖包下载完成")
            log(f"产物目录：{PKG_DIR}")
            log("如需离线安装，可执行：")
            log(
                f"  pip install --no-index --find-links={PKG_DIR} "
                + " ".join(REQUIREMENTS)
            )
            return 0

    log("所有镜像均下载失败，请检查网络后重试")
    return 1


if __name__ == "__main__":
    sys.exit(main())
