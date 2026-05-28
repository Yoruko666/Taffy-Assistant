# One-click start: ASR + TTS + Worker + Server
# 用法（在仓库根目录或 scripts/ 内均可）：
#     .\scripts\start-all.ps1
$ErrorActionPreference = "Stop"
$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$RootDir   = Split-Path -Parent $ScriptDir   # scripts/ -> 仓库根

$ModelDir  = Join-Path $RootDir "model"
$WorkerDir = Join-Path $RootDir "worker"
$ServerDir = Join-Path $RootDir "server"

$AsrPort    = if ($env:ASR_PORT)    { $env:ASR_PORT }    else { "9100" }
$TtsPort    = if ($env:TTS_PORT)    { $env:TTS_PORT }    else { "9200" }
$WorkerPort = if ($env:WORKER_PORT) { $env:WORKER_PORT } else { "8090" }
$GoPort     = if ($env:PORT)        { $env:PORT }        else { "8080" }

function NewTerm($WorkDir, $Cmd) {
    $enc = [Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes("chcp 65001|out-null; cd '$WorkDir'; $Cmd"))
    Start-Process powershell -ArgumentList "-NoExit", "-Exec", "Bypass", "-Enc", $enc
}

Write-Host "=== Taffy START ALL ==="
Write-Host ""

# start ASR
Write-Host "[1/7] Starting ASR..."
NewTerm $ModelDir "`$env:ASR_DEVICE='cuda'; python -m uvicorn asr_server.app:app --host 0.0.0.0 --port $AsrPort"

# start TTS
Write-Host "[2/7] Starting TTS..."
NewTerm $ModelDir "python -m uvicorn tts_server.app:app --host 0.0.0.0 --port $TtsPort"

# wait for ASR
Write-Host "[3/7] Waiting for ASR..."
$asrOk = $false
for ($i = 0; $i -lt 60; $i++) {
    try {
        $r = Invoke-WebRequest -UseBasicParsing -TimeoutSec 2 "http://127.0.0.1:$AsrPort/v1/health"
        if ($r.Content -match "stream_loaded") { $asrOk = $true; break }
    } catch {}
    Write-Host "." -NoNewline
    Start-Sleep 2
}
if (-not $asrOk) { Write-Host "`nASR failed to start"; exit 1 }
Write-Host "`n  [ok] ASR ready"

# wait for TTS
Write-Host "[4/7] Waiting for TTS..."
$ttsOk = $false
for ($i = 0; $i -lt 30; $i++) {
    try {
        $r = Invoke-WebRequest -UseBasicParsing -TimeoutSec 2 "http://127.0.0.1:$TtsPort/v1/health"
        $ttsOk = $true; break
    } catch {}
    Write-Host "." -NoNewline
    Start-Sleep 2
}
if (-not $ttsOk) { Write-Host "`n  [warn] TTS not ready (non-critical)" }
Write-Host "`n  [ok] TTS ready"

# start Worker（大模型编排：ASR / LLM / TTS）
Write-Host "[5/7] Starting Worker..."
NewTerm $WorkerDir "`$env:WORKER_PORT='$WorkerPort'; go run ./cmd/worker"

# wait for Worker
Write-Host "[6/7] Waiting for Worker..."
$workerOk = $false
for ($i = 0; $i -lt 30; $i++) {
    try {
        $r = Invoke-WebRequest -UseBasicParsing -TimeoutSec 2 "http://127.0.0.1:$WorkerPort/v1/health"
        if ($r.Content -match "taffy-worker") { $workerOk = $true; break }
    } catch {}
    Write-Host "." -NoNewline
    Start-Sleep 2
}
if (-not $workerOk) { Write-Host "`nWorker failed to start"; exit 1 }
Write-Host "`n  [ok] Worker ready"

# start Server（家具 / 客户端入口）
Write-Host "[7/7] Starting Server..."
NewTerm $ServerDir "`$env:PORT='$GoPort'; `$env:WORKER_WS_URL='ws://127.0.0.1:$WorkerPort/v1/orchestrate'; go run ./cmd/server"

Write-Host "`n=== ALL SERVICES STARTED ==="
Write-Host "Close each window to stop the corresponding service."
Write-Host "`nTest commands (run in project root):"
Write-Host "  # live microphone"
Write-Host ('  python furniture/mock_furniture.py --server "ws://127.0.0.1:' + $GoPort + '/v1/voice?device_id=dev1&token=t1"')
