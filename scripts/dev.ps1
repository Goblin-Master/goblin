param(
    [Parameter(Position = 0)]
    [ValidateSet("up", "down", "status", "reset")]
    [string]$Action = "up"
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot

function Find-Docker {
    $command = Get-Command docker -ErrorAction SilentlyContinue
    if ($command) {
        return $command.Source
    }
    $candidates = @(
        (Join-Path $env:LOCALAPPDATA "Programs\DockerDesktop\resources\bin\docker.exe"),
        "C:\Program Files\Docker\Docker\resources\bin\docker.exe"
    )
    foreach ($candidate in $candidates) {
        if (Test-Path $candidate) {
            return $candidate
        }
    }
    throw "找不到 docker.exe。请确认 Docker Desktop 已安装并启动。"
}

$docker = Find-Docker
$bin = Split-Path -Parent $docker
$env:PATH = "$bin;$env:PATH"

Push-Location $root
try {
    switch ($Action) {
        "up" {
            & $docker compose up -d mysql
            if ($LASTEXITCODE -ne 0) { throw "docker compose up 失败" }

            Write-Host "等待 MySQL 127.0.0.1:3306 ..."
            $ready = $false
            for ($i = 0; $i -lt 60; $i++) {
                Start-Sleep -Seconds 1
                if (Test-NetConnection -ComputerName 127.0.0.1 -Port 3306 -InformationLevel Quiet -WarningAction SilentlyContinue) {
                    $ready = $true
                    break
                }
            }
            if (-not $ready) { throw "MySQL 端口未就绪，请运行 scripts/dev.ps1 status 查看容器状态" }
            Write-Host "MySQL 已就绪：127.0.0.1:3306 / 数据库 goblin"
        }
        "down" {
            & $docker compose down
            if ($LASTEXITCODE -ne 0) { throw "docker compose down 失败" }
            Write-Host "MySQL 已停止，数据卷保留。"
        }
        "status" {
            & $docker compose ps
            if ($LASTEXITCODE -ne 0) { throw "docker compose ps 失败" }
            $open = Test-NetConnection -ComputerName 127.0.0.1 -Port 3306 -InformationLevel Quiet -WarningAction SilentlyContinue
            Write-Host ("3306 reachable: {0}" -f $open)
        }
        "reset" {
            $answer = Read-Host "这会删除 goblin_mysql_data 数据卷。输入 yes 继续"
            if ($answer -ne "yes") {
                Write-Host "已取消。"
                return
            }
            & $docker compose down -v
            if ($LASTEXITCODE -ne 0) { throw "docker compose down -v 失败" }
            Write-Host "MySQL 容器与数据卷已删除。"
        }
    }
}
finally {
    Pop-Location
}
