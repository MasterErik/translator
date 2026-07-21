Clear-Host
Write-Host "================================================ " -ForegroundColor Cyan
Write-Host "         AUDIO DIAGNOSTIC SCRIPT                 " -ForegroundColor Cyan
Write-Host "================================================ " -ForegroundColor Cyan
Write-Host ""

# 1. Check Administrator Privileges
$isAdmin = ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
if ($isAdmin) {
    Write-Host "[+] Admin Rights: YES" -ForegroundColor Green
} else {
    Write-Host "[!] Admin Rights: NO (Run PowerShell as Administrator)" -ForegroundColor Yellow
}
Write-Host ""

# 2. Windows Audio Services
Write-Host "--- 1. WINDOWS AUDIO SERVICES ---" -ForegroundColor Yellow
$services = @("Audiosrv", "AudioEndpointBuilder")
foreach ($srvName in $services) {
    $srv = Get-Service -Name $srvName -ErrorAction SilentlyContinue
    if ($srv) {
        $statusColor = if ($srv.Status -eq "Running") { "Green" } else { "Red" }
        Write-Host "Service $($srv.Name): $($srv.Status)" -ForegroundColor $statusColor
    } else {
        Write-Host "Service $srvName not found." -ForegroundColor Red
    }
}
Write-Host ""

# 3. Related Processes
Write-Host "--- 2. PROCESSES & BROWSERS ---" -ForegroundColor Yellow
$targetProcesses = @("Mixline", "audiodg", "chrome", "msedge", "browser", "yandex", "firefox", "opera")
foreach ($procName in $targetProcesses) {
    $procs = Get-Process -Name $procName -ErrorAction SilentlyContinue
    if ($procs) {
        Write-Host "Process '$procName': RUNNING ($($procs.Count) inst.)" -ForegroundColor Green
    } else {
        Write-Host "Process '$procName': NOT RUNNING" -ForegroundColor Gray
    }
}
Write-Host ""

# 4. PnP Audio Endpoints
Write-Host "--- 3. PNP AUDIO ENDPOINTS ---" -ForegroundColor Yellow
$pnpAudio = Get-PnpDevice -Class AudioEndpoint -ErrorAction SilentlyContinue | Where-Object { $_.Status -eq "OK" }
if ($pnpAudio) {
    $pnpAudio | Select-Object FriendlyName, InstanceId, Status | Format-Table -AutoSize
} else {
    Write-Host "No PnP Audio devices found!" -ForegroundColor Red
}

# 5. Sound Cards (Win32_SoundDevice)
Write-Host "--- 4. SOUND DEVICES (Win32_SoundDevice) ---" -ForegroundColor Yellow
$soundDevices = Get-CimInstance Win32_SoundDevice -ErrorAction SilentlyContinue
if ($soundDevices) {
    $soundDevices | Select-Object Name, Manufacturer, Status | Format-Table -AutoSize
} else {
    Write-Host "No sound devices found via WMI." -ForegroundColor Red
}

Write-Host "================================================" -ForegroundColor Cyan
Write-Host "Diagnostic Completed." -ForegroundColor Cyan