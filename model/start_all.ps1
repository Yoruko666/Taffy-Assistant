# 在三个新窗口中分别启动 ASR (9100) / TTS (9200) / LLM (9300) 本地服务。
# 用法：在 model/ 目录下执行 .\start_all.ps1
# 可通过环境变量覆盖：
#   $env:ASR_PORT=9101; $env:TTS_PORT=9201; $env:LLM_PORT=9301
#   $env:ASR_DEVICE='cpu'   # 默认 cuda，没 GPU 时改回 cpu
#   $env:CODEBUDDY_API_KEY, $env:CODEBUDDY_INTERNET_ENVIRONMENT, $env:CODEBUDDY_MODEL
# 然后运行 .\start_all.ps1

$ErrorActionPreference = "Stop"
$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location $ScriptDir

$AsrPort   = if ($env:ASR_PORT)   { $env:ASR_PORT }   else { "9100" }
$TtsPort   = if ($env:TTS_PORT)   { $env:TTS_PORT }   else { "9200" }
$LlmPort   = if ($env:LLM_PORT)   { $env:LLM_PORT }   else { "9300" }
$AsrDevice = if ($env:ASR_DEVICE) { $env:ASR_DEVICE } else { "cuda" }

Write-Host "[start_all] dir    : $ScriptDir"
Write-Host "[start_all] ASR    : http://127.0.0.1:$AsrPort  (device=$AsrDevice)"
Write-Host "[start_all] TTS    : http://127.0.0.1:$TtsPort"
Write-Host "[start_all] LLM    : http://127.0.0.1:$LlmPort"

# ASR 窗口
Start-Process powershell -ArgumentList @(
    "-NoExit", "-Command",
    "chcp 65001 > `$null; " +
    "`$env:PYTHONIOENCODING='utf-8'; " +
    "Set-Location '$ScriptDir'; " +
    "`$env:PYTHONPATH='$ScriptDir;' + `$env:PYTHONPATH; " +
    "`$env:ASR_DEVICE='$AsrDevice'; " +
    "Write-Host '[ASR] http://127.0.0.1:$AsrPort  device=$AsrDevice'; " +
    "python -m uvicorn asr_server.app:app --host 0.0.0.0 --port $AsrPort"
) -WindowStyle Normal

# TTS 窗口
Start-Process powershell -ArgumentList @(
    "-NoExit", "-Command",
    "chcp 65001 > `$null; " +
    "`$env:PYTHONIOENCODING='utf-8'; " +
    "Set-Location '$ScriptDir'; " +
    "`$env:PYTHONPATH='$ScriptDir;' + `$env:PYTHONPATH; " +
    "Write-Host '[TTS] http://127.0.0.1:$TtsPort'; " +
    "python -m uvicorn tts_server.app:app --host 0.0.0.0 --port $TtsPort"
) -WindowStyle Normal

# LLM Server 窗口（CodeBuddy 代理，端口 9300）
Start-Process powershell -ArgumentList @(
    "-NoExit", "-Command",
    "chcp 65001 > `$null; " +
    "`$env:PYTHONIOENCODING='utf-8'; " +
    "Set-Location '$ScriptDir'; " +
    "`$env:PYTHONPATH='$ScriptDir;' + `$env:PYTHONPATH; " +
    "`$env:LLM_SERVER_PORT='$LlmPort'; " +
    "Write-Host '[LLM] http://127.0.0.1:$LlmPort  (CodeBuddy)'; " +
    "python llm_server/app.py"
) -WindowStyle Normal

Write-Host "[start_all] ASR / TTS / LLM 已在新窗口启动，关闭对应窗口即停止服务。"
