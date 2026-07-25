# Third-party source directory

The release build downloads the official `zstd-1.5.7.tar.gz` release archive,
verifies its SHA-256 digest, and extracts it as `third_party/zstd/`.

The extracted source tree is intentionally ignored by Git because it is an
unaltered upstream dependency and can be reproduced with:

```sh
./scripts/fetch-zstd.sh
```

On Windows PowerShell:

```powershell
./scripts/fetch-zstd.ps1
```

libzstd is distributed under the BSD 3-Clause license and GPLv2 dual license.
Viper Patcher links it under the BSD option. The upstream license files are
included in binary release archives by the release workflow.


Go dependencies are resolved through `go.mod` and `go.sum`. Release workflows
run `scripts/collect-go-licenses.*` to copy top-level `LICENSE`, `COPYING`, and
`NOTICE` files from every resolved Go module into each binary archive.
