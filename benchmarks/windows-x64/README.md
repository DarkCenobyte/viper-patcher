# Windows x64 patch benchmark

This directory contains a reproducible Windows x64 benchmark for Viper-Patcher,
HDiffPatch, xdelta3, and Floating IPS.

The harness compares both Viper-Patcher distributions:

- hybrid `creator.exe` / `patcher.exe`, exercised in command-line mode with the
  actual default performance settings;
- GUI-free `creator-cli.exe` / `patcher-cli.exe`, exercised with compression
  levels 1, 3, and 9, each with one worker and automatic workers.

Every patch created during a measured repetition is applied and verified by
SHA-256 outside the creation timer. Every measured application is verified by
SHA-256 after its timer stops. A failed repetition prevents the affected group
from publishing a timing median.

## Repository layout

The recommended repository location is:

```text
benchmarks/
  windows-x64/
    README.md
    download-tools.ps1
    build-report.ps1
    run-benchmark.ps1
    run-publication-benchmark.ps1
    .gitignore
```

Generated executables and local runs are ignored. A selected result can later be
copied to a versioned directory such as:

```text
benchmarks/results/windows-x64/v1.0.0/<machine-and-date>/
```

This keeps the benchmark implementation separate from published measurements.

## Requirements

- Windows 10 or Windows 11, x64;
- PowerShell 7 or newer (`pwsh`);
- internet access to GitHub Releases;
- an SSD and at least about 15 GiB of free disk space for the publication run;
- no other CPU-, memory-, or storage-intensive workload during measurement.

A GitHub token is usually unnecessary for public releases. It is required when
using private or rate-limited GitHub Actions artifacts. The scripts accept
`GITHUB_TOKEN` or `-GitHubToken`.

## Publication benchmark for v1.0.0-rc.1

From the repository root, run:

```powershell
pwsh -NoProfile -File .\benchmarks\windows-x64\run-publication-benchmark.ps1 `
  -ViperTag v1.0.0-rc.1
```

The publication wrapper deliberately performs a fresh tool download and uses:

- 15 repetitions for 100 KB cases;
- 7 repetitions for 50 MB cases;
- 3 repetitions for 500 MB cases;
- one unreported warm-up per tool, case, phase, and profile;
- both the 50 MB and optional 500 MB datasets;
- serial execution across competing tools.

This is the recommended command for a result intended for the project README.
It is long-running by design.

### Final v1.0.0

When the final release is published, run the same command with the final tag:

```powershell
pwsh -NoProfile -File .\benchmarks\windows-x64\run-publication-benchmark.ps1 `
  -ViperTag v1.0.0
```

Results obtained from `v1.0.0-rc.1` should only be presented as final v1.0.0
results when the four Viper executable SHA-256 values are identical between the
release candidate and final release. The hashes are recorded in
`tools/tools-lock.json` and copied into every run directory. If any binary hash
changes, rerun the benchmark for `v1.0.0`.

## Quick validation run

Before a publication run, a short smoke test is useful:

```powershell
pwsh -NoProfile -File .\benchmarks\windows-x64\download-tools.ps1 `
  -ViperSource Release `
  -ViperTag v1.0.0-rc.1 `
  -Force

pwsh -NoProfile -File .\benchmarks\windows-x64\run-benchmark.ps1 `
  -SmallIterations 1 `
  -LargeIterations 1 `
  -HugeIterations 1
```

This checks downloads, architectures, command syntax, patch creation,
application, and SHA-256 validation without the 500 MB cases.

## Viper release resolution

The default source is a GitHub Release. For `v1.0.0-rc.1`, the downloader
requires these exact assets:

```text
viper-patcher_1.0.0-rc.1_windows_amd64.zip
viper-patcher-cli_1.0.0-rc.1_windows_amd64.zip
```

It also downloads and verifies `SHA256SUMS.txt` when that release asset is
present. The four extracted executables must:

- be Windows AMD64 PE files;
- report one common Viper version through `--version`;
- report one common commit;
- match the selected tag commit.

The resulting metadata, archive hashes, executable hashes, release identity,
and competitor assets are written to `tools/tools-lock.json`.

### Exact CI artifact instead of a release

A successful exact-commit CI artifact can be used before publication:

```powershell
pwsh -NoProfile -File .\benchmarks\windows-x64\download-tools.ps1 `
  -ViperSource WorkflowArtifact `
  -ViperRef master `
  -ViperBuildVersion 1.0.0 `
  -Force
```

The script searches successful `ci.yml` runs for the exact commit and requires
the versioned artifact:

```text
release-<version>-windows-amd64
```

GitHub Actions artifact downloads require an authenticated GitHub CLI or a
GitHub token.

### Previously downloaded CI artifact

```powershell
pwsh -NoProfile -File .\benchmarks\windows-x64\download-tools.ps1 `
  -ViperSource LocalArtifact `
  -ViperArtifactPath C:\path\release-1.0.0-rc.1-windows-amd64.zip `
  -Force
```

The version is inferred from the embedded hybrid and CLI-only archive names and
then checked against all four binary `--version` outputs.

## Benchmark matrix

### Viper hybrid reference

The hybrid executables receive only `--headless`. Compression and worker flags
are omitted, so the current program defaults are measured:

- compression level 3;
- `--workers 0` automatic policy;
- automatic memory, I/O profile, and window size;
- balanced creation optimization;
- referenced verification and buffered durability.

### Viper CLI-only profiles

The CLI-only binaries are measured with all combinations below for both
creation and application:

| Compression | Workers |
|---:|---:|
| 1 | 1 |
| 1 | automatic (`0`) |
| 3 | 1 |
| 3 | automatic (`0`) |
| 9 | 1 |
| 9 | automatic (`0`) |

`--workers 0` resolves to `runtime.GOMAXPROCS(0)`, capped by the logical CPU
count. The script records the visible logical processor count, any `GOMAXPROCS`
environment override, and the resulting estimate in `system.json`, every CSV,
and the generated Markdown report.

### Competitors

- HDiffPatch 5.1.2: `hdiffz -WD -s-64`, using native file or directory mode;
- xdelta3 3.2.0: default VCDIFF encode/decode with `-s`, sequential per file for
  the ten-file case;
- Floating IPS v198: exact IPS mode, sequential per file for the ten-file case.

The downloader prefers AMD64 competitor binaries. A non-AMD64 executable is
accepted only when the selected upstream release asset contains no AMD64
candidate, and the condition is prominently recorded and warned about.

Floating IPS cannot address modifications beyond the IPS format limit. Such
cases are reported as unsupported, not as failed or slow measurements.

## Synthetic datasets

The benchmark generates deterministic high-entropy data using SplitMix64:

- one 100,000-byte file;
- ten 100,000-byte files;
- one 50,000,000-byte file;
- one 500,000,000-byte file when `-Include500MB` is enabled.

Each shape is tested with:

- `scattered`: exactly 0.1% of bytes changed and evenly distributed;
- `unrelated`: an independent deterministic byte stream.

These datasets are reproducible and useful for controlled comparisons, but they
do not represent every production workload. Published claims should state that
the inputs are synthetic high-entropy data and should be complemented by real
executables, archives, databases, game assets, or directory trees.

## Timing policy

Included in measured time:

- process startup;
- patch creation or application;
- the complete sequential process series for multi-file xdelta3 and IPS cases.

Excluded from measured time:

- dataset generation;
- directory preparation and source copying;
- SHA-256 calculation;
- post-creation application validation;
- patch-size inspection;
- CSV and Markdown generation.

No competing tools run concurrently. The harness does not attempt to flush the
Windows filesystem cache, because doing so reliably requires privileges and can
introduce another source of bias. The warm-up therefore makes the publication
run primarily a repeatable warm-cache comparison.

## Outputs

Each run creates `runs/<timestamp>-<label>/` containing:

- `results.csv`: one row per repetition, including exact command, time, CPU,
  peak memory, patch size, validation status, and diagnostic output;
- `summary.csv`: full aggregated matrix with minimum, median, maximum, P95, and
  patch-size statistics;
- `fair-comparison.csv`: hybrid defaults, CLI-only default-equivalent settings,
  and one reference profile per competitor;
- `report.md`: complete publication-oriented Markdown tables;
- `README-snippet.md`: compact 50 MB and 500 MB tables ready to adapt for the
  root README;
- `system.json`: hardware, OS, iteration policy, automatic-worker estimate,
  executable hashes, and fairness metadata;
- `tools-lock.json`: exact tags, assets, architectures, commits, and hashes;
- `expected-hashes.json`: expected hashes for every target dataset;
- `run-command.txt`: canonical benchmark command;
- `patches/`: the final successful patch for each tool/profile/case.

Datasets and temporary work directories are deleted after a successful run
unless `-KeepDatasets` is passed.

## Rebuilding Markdown without rerunning measurements

The CSV files are written before the Markdown reports. If reporting is
interrupted after `summary.csv` and `fair-comparison.csv` have been completed,
regenerate the reports from the existing run directory:

```powershell
pwsh -NoProfile -File .\benchmarks\windows-x64\build-report.ps1 `
  -RunDirectory .\benchmarks\windows-x64\runs\20260803-202216-publication
```

This only reads the completed CSV and metadata files. It does not download tools,
create datasets, or repeat any timed operation.

## Publishing a selected result

1. Review `report.md`, `summary.csv`, `results.csv`, and all failure statuses.
2. Confirm all intended groups are `success` or explicitly documented as
   unsupported.
3. Check that the system was not under unrelated load and that no thermal or
   power throttling occurred.
4. Copy the selected run to a stable versioned result directory.
5. Commit at least `report.md`, `README-snippet.md`, `summary.csv`,
   `fair-comparison.csv`, `system.json`, `tools-lock.json`, and
   `run-command.txt`. Raw `results.csv` is strongly recommended.
6. Paste or adapt `README-snippet.md` into the root README and link to the full
   committed report.

A suitable root README introduction is:

```markdown
## Benchmarks

A reproducible Windows x64 harness compares the hybrid and CLI-only
Viper-Patcher executables with HDiffPatch, xdelta3, and Floating IPS. Every
created patch is applied and SHA-256 verified outside the measured creation
interval, and every measured application is SHA-256 verified after timing.
See [the benchmark methodology](benchmarks/windows-x64/README.md) and the
[published results](benchmarks/results/windows-x64/).
```

## Useful options

Direct benchmark invocation:

```powershell
pwsh -NoProfile -File .\benchmarks\windows-x64\run-benchmark.ps1 `
  -SmallIterations 7 `
  -LargeIterations 5 `
  -HugeIterations 2 `
  -Include500MB `
  -RunLabel local-test
```

Additional options:

- `-NoWarmup`: disables warm-ups;
- `-KeepDatasets`: preserves generated inputs and temporary work directories;
- `-ToolsDir`: selects another normalized binary directory;
- `-OutputRoot`: changes the run output directory;
- `-RunLabel`: adds a sanitized label to the timestamped run directory;
- `run-publication-benchmark.ps1 -Skip500MB`: omits the 500 MB cases.

## Limitations

This harness does not establish a universal ranking. It does not measure energy
consumption, physical disk writes, cold-cache behavior, HDD performance,
contention, rollback quality, corruption resistance, or every input type. Patch
formats also provide different features and guarantees. Interpret creation
speed, application speed, patch size, memory use, and supported cases
separately rather than combining them into one score.
