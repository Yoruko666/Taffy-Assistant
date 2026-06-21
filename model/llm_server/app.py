"""LLM Server — OpenAI 兼容的 Chat Completions API，底层使用 CodeBuddy Agent SDK。

接口:
    GET  /v1/health              健康检查
    POST /v1/chat/completions    兼容 OpenAI Chat Completions 格式

此服务接收来自 Worker 的 OpenAI 格式请求，通过 CodeBuddy SDK 的 query()
与 CodeBuddy 大模型交互，并返回标准 OpenAI 格式的响应。

配置来源（优先级：请求头 > 环境变量）:
    X-CodeBuddy-Api-Key              - CodeBuddy API Key
    X-CodeBuddy-Internet-Environment - 网络环境（如 ioa）
    model 字段（请求体 JSON）          - 模型名（如 claude-sonnet-4.6）

环境变量:
    CODEBUDDY_API_KEY              - 默认 API Key
    CODEBUDDY_INTERNET_ENVIRONMENT - 默认网络环境
    CODEBUDDY_MODEL                - 默认模型名
    LLM_SERVER_PORT                - 服务端口（默认 9300）

调用示例（curl）:
    curl -X POST http://127.0.0.1:9300/v1/chat/completions \\
        -H "Content-Type: application/json" \\
        -H "X-CodeBuddy-Api-Key: ck_xxx" \\
        -H "X-CodeBuddy-Internet-Environment: ioa" \\
        -d '{"model":"claude-sonnet-4.6","messages":[{"role":"system","content":"你是助手"},{"role":"user","content":"你好"}]}'
"""

from __future__ import annotations

import asyncio
import json
import logging
import os
import time
import uuid
from typing import Any, AsyncGenerator, Optional

from fastapi import FastAPI, HTTPException, Request
from fastapi.responses import JSONResponse
from pydantic import BaseModel, Field

from codebuddy_agent_sdk import CodeBuddyAgentOptions, query

# ---------------------------------------------------------------------------
# Logging
# ---------------------------------------------------------------------------
logging.basicConfig(
    level=logging.INFO,
    format="[llm_server] %(asctime)s %(levelname)s %(message)s",
)
logger = logging.getLogger("llm_server")

# ---------------------------------------------------------------------------
# FastAPI
# ---------------------------------------------------------------------------
app = FastAPI(title="Taffy LLM Server (CodeBuddy)", version="0.1.0")


# ---------------------------------------------------------------------------
# Pydantic models (OpenAI Chat Completions subset)
# ---------------------------------------------------------------------------
class ChatMessage(BaseModel):
    role: str = Field(..., description="system / user / assistant / tool")
    content: str = Field(default="", description="消息正文")


class ChatCompletionRequest(BaseModel):
    model: Optional[str] = Field(default=None, description="模型名，优先于环境变量")
    messages: list[ChatMessage] = Field(..., min_length=1)
    stream: bool = Field(default=False, description="暂不支持 streaming，填 true 会自动转为非流式")


class ChoiceMessage(BaseModel):
    role: str = "assistant"
    content: str = ""


class Choice(BaseModel):
    index: int = 0
    message: ChoiceMessage
    finish_reason: str = "stop"


class ChatCompletionResponse(BaseModel):
    id: str = ""
    object: str = "chat.completion"
    created: int = 0
    model: str = ""
    choices: list[Choice] = []


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------
def _resolve_options(
    request: Request,
    body_model: Optional[str],
) -> CodeBuddyAgentOptions:
    """从请求头 + 环境变量 + 请求体解析 CodeBuddy Agent 选项。

    优先级：请求头 > 环境变量
    """
    # API Key
    api_key = request.headers.get(
        "X-CodeBuddy-Api-Key",
        os.environ.get("CODEBUDDY_API_KEY", ""),
    )
    if not api_key:
        raise HTTPException(
            status_code=400,
            detail="缺少 API Key，请设置 X-CodeBuddy-Api-Key 请求头或 CODEBUDDY_API_KEY 环境变量",
        )

    # Internet environment
    internet_env = request.headers.get(
        "X-CodeBuddy-Internet-Environment",
        os.environ.get("CODEBUDDY_INTERNET_ENVIRONMENT", ""),
    )

    # Model
    model = body_model or os.environ.get("CODEBUDDY_MODEL", "")

    env_dict: dict[str, str] = {
        "CODEBUDDY_API_KEY": api_key,
    }
    if internet_env:
        env_dict["CODEBUDDY_INTERNET_ENVIRONMENT"] = internet_env

    return CodeBuddyAgentOptions(env=env_dict, model=model)


def _messages_to_prompt(messages: list[ChatMessage]) -> str:
    """将 OpenAI 消息列表拼成单段 prompt（CodeBuddy query 只接受单段文本）。

    规则：
    - 所有 system 消息内容拼接为系统指令
    - 最后一条 user 消息作为用户输入
    - 其余消息合并为对话历史（当前忽略）
    """
    system_parts: list[str] = []
    user_parts: list[str] = []

    for msg in messages:
        text = (msg.content or "").strip()
        if not text:
            continue
        if msg.role == "system":
            system_parts.append(text)
        elif msg.role == "user":
            user_parts.append(text)

    prompt = ""
    if system_parts:
        prompt += "\n\n".join(system_parts) + "\n\n"
    if user_parts:
        prompt += "\n\n".join(user_parts)

    return prompt.strip()


def _make_error_response(message: str) -> JSONResponse:
    """构造 OpenAI 风格的错误响应。"""
    body = {
        "error": {
            "message": message,
            "type": "internal_error",
            "param": None,
            "code": None,
        }
    }
    return JSONResponse(status_code=500, content=body)


# ---------------------------------------------------------------------------
# Endpoints
# ---------------------------------------------------------------------------
@app.get("/v1/health")
async def health():
    """健康检查。"""
    return {
        "status": "ok",
        "codebuddy_configured": bool(os.environ.get("CODEBUDDY_API_KEY")),
    }


@app.post("/v1/chat/completions")
async def chat_completions(request: Request):
    """OpenAI 兼容的 Chat Completions 接口。

    底层通过 CodeBuddy SDK 的 query() 与 CodeBuddy 大模型交互。
    当前仅支持非流式响应（stream=false）。
    """
    # 1) 解析请求体
    try:
        body_raw = await request.json()
    except Exception as exc:
        logger.warning("解析请求体失败: %s", exc)
        return _make_error_response(f"无效的 JSON 请求体: {exc}")

    try:
        req = ChatCompletionRequest(**body_raw)
    except Exception as exc:
        logger.warning("请求体验证失败: %s", exc)
        return _make_error_response(f"请求体验证失败: {exc}")

    # 2) 提取模型名（body 中的 model 优先于环境变量）
    model_name = req.model or os.environ.get("CODEBUDDY_MODEL", "unknown")

    # 3) 构建 CodeBuddy options
    try:
        options = _resolve_options(request, req.model)
    except HTTPException:
        raise
    except Exception as exc:
        logger.error("构建 CodeBuddy 选项失败: %s", exc)
        return _make_error_response(f"构建 CodeBuddy 选项失败: {exc}")

    # 4) 拼接 prompt
    prompt = _messages_to_prompt(req.messages)
    if not prompt:
        return _make_error_response("messages 内容为空")

    logger.info(
        "request: model=%s system=%s user=%s",
        model_name,
        bool(any(m.role == "system" for m in req.messages)),
        prompt[-80:] if len(prompt) > 80 else prompt,
    )

    # 5) 调用 CodeBuddy SDK
    t0 = time.time()
    try:
        reply_text = ""
        async for msg in query(prompt=prompt, options=options):
            if msg is None:
                continue
            msg_type = type(msg).__name__

            if msg_type == "AssistantMessage":
                # AssistantMessage.content 是 TextBlock 列表
                blocks = getattr(msg, "content", [])
                for block in blocks:
                    block_type = type(block).__name__
                    if block_type == "TextBlock":
                        reply_text += getattr(block, "text", str(block))
                    else:
                        reply_text += str(block)

            elif msg_type == "ResultMessage":
                # ResultMessage.result 是最终纯文本
                result = getattr(msg, "result", "")
                if result:
                    reply_text = result

            # SystemMessage 等系统消息直接跳过
    except Exception as exc:
        logger.exception("CodeBuddy query 调用失败")
        return _make_error_response(f"CodeBuddy query 调用失败: {exc}")

    elapsed = time.time() - t0
    logger.info(
        "response: model=%s reply_len=%d cost=%.2fs",
        model_name,
        len(reply_text),
        elapsed,
    )

    # 6) 构造 OpenAI 格式响应
    resp = ChatCompletionResponse(
        id=f"chatcmpl-{uuid.uuid4().hex[:12]}",
        created=int(t0),
        model=model_name,
        choices=[
            Choice(
                message=ChoiceMessage(content=reply_text),
            )
        ],
    )
    return resp.model_dump()


if __name__ == "__main__":
    import uvicorn

    port = int(os.environ.get("LLM_SERVER_PORT", "9300"))
    logger.info("启动 LLM Server，端口 %d", port)
    uvicorn.run(
        app,
        host="0.0.0.0",
        port=port,
    )
