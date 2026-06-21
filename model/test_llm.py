"""LLM 单测脚本：直接打阿里百炼，验证 tool_call 是否能正确触发。
用法：
    $env:LLM_API_KEY = "sk-xxx"
    python model/test_llm.py
    python model/test_llm.py "把卧室灯调到最亮"   # 自定义输入
"""
import os
import sys
import json
import requests

URL = os.environ.get(
    "LLM_URL",
    "https://dashscope.aliyuncs.com/compatible-mode/v1/chat/completions",
)
MODEL = os.environ.get("LLM_MODEL", "qwen-turbo")
KEY = os.environ.get("LLM_API_KEY", "")

if not KEY or KEY.startswith("sk-your"):
    print("[!] 请先设置环境变量 LLM_API_KEY")
    sys.exit(1)

# 复刻 worker/internal/handler/prompt.go 的 system prompt 结构
SYSTEM_PROMPT = """你是「小菲」，一个智能家居语音助手。请用中文简短回答用户的问题。

你可以控制下列设备（仅限这些）：
- light-001 / 客厅灯 / light / living_room
- aircon-001 / 客厅空调 / aircon / living_room
- curtain-001 / 卧室窗帘 / curtain / bedroom
- light-002 / 卧室灯 / light / bedroom

控制规则：
- 用户提到打开/关闭/调节设备时，必须调用 control_device 工具，不要只回复文字。
- 设备不在列表内时，直接回复「抱歉，您的设备列表里没有 xxx」，不要调用 tool。
- 闲聊（如天气、问答）不要调用 tool，直接回复。
"""

# 复刻 worker/internal/handler/tools.go 的 tools 定义
TOOLS = [{
    "type": "function",
    "function": {
        "name": "control_device",
        "description": "控制智能家居设备",
        "parameters": {
            "type": "object",
            "properties": {
                "device_id": {"type": "string", "description": "设备ID"},
                "action": {
                    "type": "string",
                    "enum": ["turn_on", "turn_off", "set_temperature",
                             "set_brightness", "set_position"],
                    "description": "操作类型",
                },
                "value": {"type": "number", "description": "数值参数（温度/亮度/位置）"},
            },
            "required": ["device_id", "action"],
        },
    },
}]

CASES = [
    "打开客厅灯",
    "把空调调到26度",
    "卧室窗帘拉到一半",
    "今天天气怎么样",       # 应不调 tool
    "把厨房灯打开",         # 应拒绝（设备不在列表）
]


def call(user_text: str) -> dict:
    payload = {
        "model": MODEL,
        "messages": [
            {"role": "system", "content": SYSTEM_PROMPT},
            {"role": "user", "content": user_text},
        ],
        "tools": TOOLS,
        "stream": False,
    }
    r = requests.post(
        URL,
        headers={
            "Authorization": f"Bearer {KEY}",
            "Content-Type": "application/json",
        },
        json=payload,
        timeout=30,
    )
    return r.json()


def show(user_text: str):
    print(f"\n=== 用户：{user_text} ===")
    try:
        data = call(user_text)
    except Exception as e:
        print("  [HTTP ERROR]", e)
        return

    if "error" in data:
        print("  [API ERROR]", data["error"])
        return

    choice = data["choices"][0]["message"]
    content = choice.get("content") or ""
    tool_calls = choice.get("tool_calls") or []

    if tool_calls:
        for tc in tool_calls:
            fn = tc.get("function", {})
            name = fn.get("name")
            args = fn.get("arguments", "{}")
            try:
                args_obj = json.loads(args)
            except Exception:
                args_obj = args
            print(f"  [TOOL] {name}  {args_obj}")
        if content.strip():
            print(f"  [TEXT] {content}")
    else:
        print(f"  [REPLY] {content}")


if __name__ == "__main__":
    if len(sys.argv) > 1:
        show(" ".join(sys.argv[1:]))
    else:
        for c in CASES:
            show(c)
