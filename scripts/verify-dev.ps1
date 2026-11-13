# Explicit Windows smoke test for the development supervisor.
# Creates only fixture-owned processes and files; never opens network listeners.
[CmdletBinding()]
param([string]$FramePath = '')
$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path -Parent $PSScriptRoot
$fixtureDir = Join-Path ([System.IO.Path]::GetTempPath()) ('frame-dev-smoke-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $fixtureDir | Out-Null
$sourcePath = Join-Path $fixtureDir 'main.go'
$statusPath = Join-Path $fixtureDir 'status.txt'
$childPath = Join-Path $fixtureDir 'child.txt'
$stdoutPath = Join-Path $fixtureDir 'dev.stdout'
$stderrPath = Join-Path $fixtureDir 'dev.stderr'
$utf8 = [System.Text.UTF8Encoding]::new($false)
$sourceTemplate = @'
package main
import ("fmt"; "os"; "os/exec"; "strconv"; "syscall"; "time")
func main() {
 if len(os.Args)>1 { for { time.Sleep(time.Second) } }
 // A replacement must not begin while the previous service's child is alive.
 if previous,err:=os.ReadFile("child.txt");err==nil {
  pid,err:=strconv.Atoi(string(previous));if err!=nil{panic(err)}
  if handle,err:=syscall.OpenProcess(0x1000,false,uint32(pid));err==nil {
   var code uint32;queryErr:=syscall.GetExitCodeProcess(handle,&code);syscall.CloseHandle(handle)
   if queryErr!=nil{panic(queryErr)};if code==259{panic("replacement started before previous child exited")}
  } else if err!=syscall.Errno(87){panic(err)}
 }
 child:=exec.Command(os.Args[0],"child"); if err:=child.Start();err!=nil{panic(err)}
 _=os.WriteFile("child.txt",[]byte(fmt.Sprint(child.Process.Pid)),0600)
 for { _=os.WriteFile("status.txt",[]byte(fmt.Sprintf("__VERSION__,%d,%d",os.Getpid(),time.Now().UnixNano())),0600); time.Sleep(50*time.Millisecond) }
}
'@
function Write-App([string]$version, [switch]$Atomic) {
    $target = $sourcePath
    if ($Atomic) { $target = Join-Path $fixtureDir 'replacement.txt' }
    [System.IO.File]::WriteAllText($target, $sourceTemplate.Replace('__VERSION__', $version), $utf8)
    if ($Atomic) { Move-Item -LiteralPath $target -Destination $sourcePath -Force }
}
function Read-Status {
    try { return [System.IO.File]::ReadAllText($statusPath).Split(',') } catch { return @() }
}
function Read-Log([string]$path) {
    $stream = [System.IO.File]::Open($path, [System.IO.FileMode]::Open, [System.IO.FileAccess]::Read, [System.IO.FileShare]::ReadWrite)
    $reader = [System.IO.StreamReader]::new($stream)
    try { return $reader.ReadToEnd() } finally { $reader.Dispose() }
}
function Wait-Version([string]$version) {
    $deadline = [DateTime]::UtcNow.AddSeconds(30)
    do {
        $status = @(Read-Status)
        if ($status.Count -eq 3 -and $status[0] -eq $version) { return $status }
        if ($runner.HasExited) { throw 'Supervisor exited before expected fixture version' }
        Start-Sleep -Milliseconds 100
    } while ([DateTime]::UtcNow -lt $deadline)
    throw "Timed out waiting for fixture version $version; inspect $fixtureDir"
}
function Assert-Exited([int]$processID) {
    $deadline = [DateTime]::UtcNow.AddSeconds(5)
    while ([DateTime]::UtcNow -lt $deadline) {
        if (-not (Get-Process -Id $processID -ErrorAction SilentlyContinue)) { return }
        Start-Sleep -Milliseconds 100
    }
    throw "Fixture process $processID survived cleanup"
}

[System.IO.File]::WriteAllText((Join-Path $fixtureDir 'go.mod'), "module example.com/devsmoke`n`ngo 1.27.1`n", $utf8)
if (-not $FramePath) {
    $FramePath = Join-Path $fixtureDir 'frame.exe'
    Push-Location $repoRoot
    try {
        & go build -buildvcs=false -o $FramePath ./cmd/frame
        if ($LASTEXITCODE -ne 0) { throw 'Could not build development supervisor' }
    } finally { Pop-Location }
}
$FramePath = (Resolve-Path -LiteralPath $FramePath).Path
Write-App 'one'
$runner = $null
$lastApp = 0
$lastChild = 0
try {
    $runner = Start-Process -FilePath $FramePath -ArgumentList @('dev', '--dir', ('"' + $fixtureDir + '"'), '--package', '.', '--interval', '100ms', '--debounce', '150ms', '--stop-timeout', '2s', '--') -WorkingDirectory $fixtureDir -WindowStyle Hidden -RedirectStandardOutput $stdoutPath -RedirectStandardError $stderrPath -PassThru
    $first = @(Wait-Version 'one')
    $firstChild = [int][System.IO.File]::ReadAllText($childPath)
    $lastApp = [int]$first[1]
    $lastChild = $firstChild
    Write-Output 'Initial build passed.'
    Write-App 'two'
    $second = @(Wait-Version 'two')
    $lastApp = [int]$second[1]
    $lastChild = [int][System.IO.File]::ReadAllText($childPath)
    Assert-Exited ([int]$first[1])
    Assert-Exited $firstChild
    Write-Output 'Successful edit replaced the process and its child.'
    [System.IO.File]::WriteAllText($sourcePath, 'package main; func main( {', $utf8)
    $deadline = [DateTime]::UtcNow.AddSeconds(30)
    $failed = $false
    do {
        if (((Read-Log $stdoutPath) + (Read-Log $stderrPath)) -match 'failed to build|syntax error|build failed') { $failed = $true; break }
        Start-Sleep -Milliseconds 100
    } while ([DateTime]::UtcNow -lt $deadline)
    if (-not $failed) { throw 'Broken build did not report failure' }
    $before = @(Wait-Version 'two')
    Start-Sleep -Milliseconds 250
    $after = @(Wait-Version 'two')
    if ($after.Count -ne 3 -or $after[0] -ne 'two' -or $after[1] -ne $second[1] -or $after[2] -eq $before[2]) { throw 'Broken build stopped the old process' }
    Write-Output 'Broken build kept the previous process working.'
    Write-App 'three' -Atomic
    $null = Wait-Version 'three'
    Write-App 'four'
    Start-Sleep -Milliseconds 50
    Write-App 'five'
    $last = @(Wait-Version 'five')
    $lastApp = [int]$last[1]
    $lastChild = [int][System.IO.File]::ReadAllText($childPath)
    [System.IO.File]::WriteAllText((Join-Path $fixtureDir '.frame-dev/ignored.go'), 'broken ignored source', $utf8)
    Start-Sleep -Milliseconds 1500
    $unchanged = @(Wait-Version 'five')
    if ($unchanged.Count -ne 3 -or $unchanged[1] -ne $last[1]) { throw 'Build output change retriggered the runner' }
    Write-Output 'Atomic replacement, rapid edits and output-directory exclusion passed.'
} finally {
    if ($runner -and -not $runner.HasExited) {
        # Kill only the supervisor, proving its Windows Job cleanup also stops
        # descendants even when no graceful shutdown handler can run.
        Stop-Process -Id $runner.Id -Force -ErrorAction Stop
        $runner.WaitForExit(5000) | Out-Null
    }
    if ($lastApp) { Assert-Exited $lastApp }
    if ($lastChild) { Assert-Exited $lastChild }
    Write-Output "Fixture diagnostics retained at $fixtureDir"
}
Write-Output 'Windows supervisor smoke passed. Abrupt supervisor-exit cleanup was tested; interactive Ctrl+C was not simulated.'
