# NP/2 Comparative Lab

The lab command records matched direct, NP/2, and Xray measurements without
putting credentials, private carrier paths, URLs, or raw network errors in the
result file.

Build the runner:

```powershell
go build -o .tools/comparative/neproto-lab.exe ./cmd/neproto-lab
```

Generate isolated Xray configurations with a pinned Xray binary. Generated
files contain temporary keys and belong under the ignored `.tools` directory:

```powershell
./tests/comparative/prepare-xray-lab.ps1 `
  -Xray .tools/comparative/xray-v26.3.27/windows/xray.exe `
  -OutputDirectory .tools/comparative/xray-local
```

Record a direct block:

```powershell
.tools/comparative/neproto-lab.exe measure `
  --run-id 20260719-windows `
  --implementation direct --profile baseline --transport direct `
  --network windows-current --endpoint vps-50mb `
  --url https://BENCHMARK_HOST/OBJECT --expected-bytes 50000000 `
  --ip-version 4 --runs 20 `
  --output .tools/comparative/20260719-windows/samples.jsonl
```

For a running NP/2 or Xray local SOCKS listener, add for example:

```text
--proxy socks5h://127.0.0.1:1081
```

Summarize all blocks in the same JSONL file:

```powershell
.tools/comparative/neproto-lab.exe summarize `
  --input .tools/comparative/20260719-windows/samples.jsonl `
  --json .tools/comparative/20260719-windows/report.json `
  --markdown .tools/comparative/20260719-windows/report.md
```

The desktop/VPS stage does not establish censorship resistance. See
`docs/NP2-VLESS-COMPARATIVE-LAB-SPEC.md` for capture, classifier, active-probe,
and censored-network gates.

Do not mix IPv4 and IPv6 samples in one comparison. The runner defaults to
IPv4 because a direct client and a SOCKS server can otherwise select different
CDN address families and produce a routing comparison instead of a protocol
comparison. For CDN targets, resolve one address before the experiment and pass
the same `--target-address` IP to every candidate block. The address is used for
dialing but is not written to the sample artifact.
