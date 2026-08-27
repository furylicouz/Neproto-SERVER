Set-StrictMode -Version Latest

function ConvertFrom-FlutterMachineVersionOutput {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)]
        [AllowEmptyCollection()]
        [object[]]$Output
    )

    $lines = @($Output | ForEach-Object { [string]$_ })
    $jsonStart = -1
    for ($index = 0; $index -lt $lines.Count; $index++) {
        if ($lines[$index].TrimStart().StartsWith("{")) {
            $jsonStart = $index
            break
        }
    }
    if ($jsonStart -lt 0) {
        throw "Flutter --version --machine did not produce JSON"
    }

    $json = ($lines[$jsonStart..($lines.Count - 1)] -join "`n").Trim()
    try {
        return $json | ConvertFrom-Json -ErrorAction Stop
    } catch {
        throw "Flutter --version --machine produced invalid JSON"
    }
}
