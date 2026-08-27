Set-StrictMode -Version Latest

function ConvertTo-NeProtoSemanticVersion([string]$Value) {
    if ($Value -notmatch '^np2-([0-9]+\.[0-9]+\.[0-9]+)$') {
        throw "Invalid NeProto service version: $Value"
    }
    return [Version]$Matches[1]
}

function Get-NeProtoServiceVersion([string]$Path) {
    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) {
        throw "NeProto Windows service is missing at $Path"
    }

    $output = @(& $Path --version 2>&1)
    $exitCode = $LASTEXITCODE
    $versionOutput = ($output -join "`n").Trim()
    if ($exitCode -ne 0) {
        throw "NeProto Windows service version probe failed at $Path with exit code $exitCode"
    }
    if ($versionOutput -notmatch '^NeProto Windows Service (np2-[0-9]+\.[0-9]+\.[0-9]+)$') {
        throw "NeProto Windows service returned invalid version output at $Path"
    }
    return $Matches[1]
}

function Assert-NeProtoServiceVersion([string]$Path, [string]$ExpectedVersion) {
    [void](ConvertTo-NeProtoSemanticVersion $ExpectedVersion)
    $actualVersion = Get-NeProtoServiceVersion $Path
    if ($actualVersion -ne $ExpectedVersion) {
        throw "NeProto Windows service version mismatch at $Path`: expected $ExpectedVersion, found $actualVersion"
    }
}

function Assert-NeProtoStableBaseCompatible([string]$StableVersion, [string]$CandidateVersion) {
    $stableSemanticVersion = ConvertTo-NeProtoSemanticVersion $StableVersion
    $candidateSemanticVersion = ConvertTo-NeProtoSemanticVersion $CandidateVersion
    if ($stableSemanticVersion.Major -ne $candidateSemanticVersion.Major -or
        $stableSemanticVersion.Minor -ne $candidateSemanticVersion.Minor) {
        throw "Installed NeProto service $StableVersion is from a different release line than candidate $CandidateVersion"
    }
    if ($stableSemanticVersion -gt $candidateSemanticVersion) {
        throw "Installed NeProto service $StableVersion is newer than candidate $CandidateVersion; refusing downgrade"
    }
}

function Get-NeProtoRollbackBaseVersion([object]$State) {
    $baseVersion = [string]$State.version
    if ($State.PSObject.Properties.Name -contains "base_version") {
        $baseVersion = [string]$State.base_version
    }
    [void](ConvertTo-NeProtoSemanticVersion $baseVersion)
    return $baseVersion
}
