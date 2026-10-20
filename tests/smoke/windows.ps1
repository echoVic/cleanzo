$ErrorActionPreference = 'Stop'
$env:CLEANZO_SMOKE_DIR = Join-Path $env:RUNNER_TEMP 'cleanzo-smoke'
New-Item -ItemType Directory -Path $env:CLEANZO_SMOKE_DIR -Force | Out-Null
$app = Start-Process "$env:LOCALAPPDATA\Programs\Cleanzo\Cleanzo.exe" -PassThru
try {
  if (!$app.WaitForExit(300000)) { throw 'Smoke tour exceeded five minutes' }
  $app.WaitForExit()
  if ($app.ExitCode -ne 0) { throw "Smoke tour exited with $($app.ExitCode)" }
  $report = Join-Path $env:CLEANZO_SMOKE_DIR 'errors.json'
  if (!(Test-Path $report)) { throw 'Smoke error report missing' }
  $raw = Get-Content $report -Raw
  if ($raw.Trim() -ne '[]') { throw "Smoke errors: $raw" }
  $views = 'status', 'clean', 'aitools', 'skills', 'fullscan', 'large', 'photos', 'apps', 'settings'
  foreach ($theme in 'light', 'dark') {
    foreach ($view in $views) {
      $image = Join-Path $env:CLEANZO_SMOKE_DIR "$theme-$view.png"
      if (!(Test-Path $image) -or (Get-Item $image).Length -eq 0) {
        throw "Missing screenshot: $theme-$view.png"
      }
    }
  }
} finally {
  if (!$app.HasExited) { taskkill /PID $app.Id /T /F | Out-Null }
}
