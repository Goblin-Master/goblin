param()

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
$errors = [System.Collections.Generic.List[string]]::new()

$docFiles = @()
$docFiles += Get-Item -LiteralPath (Join-Path $root "README.md")
$docFiles += Get-Item -LiteralPath (Join-Path $root "GLOSSARY.md")
$docFiles += Get-ChildItem -LiteralPath (Join-Path $root "docs") -Recurse -File -Filter "*.md"

foreach ($file in $docFiles) {
    $text = Get-Content -LiteralPath $file.FullName -Raw
    foreach ($match in [regex]::Matches($text, '\]\(([^)]+)\)')) {
        $target = $match.Groups[1].Value.Trim()
        if ($target -match '^(https?:|mailto:|#)') {
            continue
        }
        $pathPart = ($target -split '#', 2)[0]
        if ([string]::IsNullOrWhiteSpace($pathPart)) {
            continue
        }
        $resolved = Join-Path (Split-Path -Parent $file.FullName) $pathPart
        if (-not (Test-Path -LiteralPath $resolved)) {
            $errors.Add("broken link in $($file.FullName): $target")
        }
    }
}

$staleFiles = @(
    (Join-Path $root "README.md"),
    (Join-Path $root "GLOSSARY.md"),
    (Join-Path $root "docs\spec.md"),
    (Join-Path $root "docs\testing.md"),
    (Join-Path $root "docs\README.md"),
    (Join-Path $root "docs\adr\0001-mysql-only-persistence.md"),
    (Join-Path $root "docs\adr\0002-native-tool-calls.md")
)
$staleTerms = @(
    "goblin_dev",
    "root_dev",
    "GOBLIN_STORAGE",
    "flag.NewFlagSet",
    "JSONStore",
    "storage.driver",
    "contextmgr",
    "weather(mock)"
)
foreach ($file in $staleFiles) {
    if (-not (Test-Path -LiteralPath $file)) {
        continue
    }
    $text = Get-Content -LiteralPath $file -Raw
    foreach ($term in $staleTerms) {
        if ($text.Contains($term)) {
            $errors.Add("stale term '$term' in $file")
        }
    }
}

if (Test-Path -LiteralPath (Join-Path $root ".env")) {
    $ignored = & git -C $root check-ignore --quiet ".env"
    if ($LASTEXITCODE -ne 0) {
        $errors.Add(".env exists but is not ignored by git")
    }
}

if ($errors.Count -gt 0) {
    Write-Host "文档检查失败：" -ForegroundColor Red
    foreach ($item in $errors) {
        Write-Host " - $item"
    }
    exit 1
}

Write-Host "文档检查通过：链接、过期术语、.env 忽略状态正常。"
