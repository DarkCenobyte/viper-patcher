# Windows x64 benchmark report

Generated: 2026-08-03 21:11:41 UTC

- Viper-Patcher: `1.0.0-rc.1` at commit `48dbd5d3f129fa76587b01f38f2caf24c7dcb26d`
- CPU: Intel(R) Core(TM) i7-10700 CPU @ 2.90GHz
- Logical processors: 16; Viper automatic worker estimate: 16
- Memory: 31.92 GiB
- OS: Microsoft Windows 11 Professionnel 10.0.26200
- Iterations: small=15, large=7, huge=3; warm-up=True
- Command: `pwsh -NoProfile -File .\benchmarks\windows-x64\run-benchmark.ps1 -SmallIterations 15 -LargeIterations 7 -HugeIterations 3 -Include500MB -RunLabel "publication"`

Every created patch was applied and SHA-256 checked outside the creation timer. Every measured application was SHA-256 checked after its timer stopped. A failed repetition prevents a timing median from being published for that group.

## Reference comparison

This view contains the Viper hybrid defaults, the CLI-only default-equivalent profile, and one documented reference profile per competitor. See `summary.csv` for every Viper tuning profile.

### `one_100KB_scattered`

| Tool | Profile | Create median | Apply median | Patch size |
|---|---|---:|---:|---:|
| Floating IPS 198 | `single_ips_exact` | 14.820 ms | 15.130 ms | 608 bytes |
| HDiffPatch 5.1.2 | `WD_s64_reference` | 24.997 ms | 19.714 ms | 241 bytes |
| Viper-Patcher CLI-only 1.0.0-rc.1 | `cli_compression_3_workers_auto` | 66.170 ms | 37.079 ms | 1,006 bytes |
| Viper-Patcher hybrid 1.0.0-rc.1 | `hybrid_defaults_compression3_workers_auto` | 154.805 ms | 133.081 ms | 1,006 bytes |
| xdelta3 3.2.0 | `single_default` | 21.953 ms | 20.318 ms | 638 bytes |

### `one_100KB_unrelated`

| Tool | Profile | Create median | Apply median | Patch size |
|---|---|---:|---:|---:|
| Floating IPS 198 | `single_ips_exact` | 15.539 ms | 15.127 ms | 97.674 KiB (100,018 bytes) |
| HDiffPatch 5.1.2 | `WD_s64_reference` | 37.611 ms | 20.164 ms | 97.755 KiB (100,101 bytes) |
| Viper-Patcher CLI-only 1.0.0-rc.1 | `cli_compression_3_workers_auto` | 78.042 ms | 38.276 ms | 98.163 KiB (100,519 bytes) |
| Viper-Patcher hybrid 1.0.0-rc.1 | `hybrid_defaults_compression3_workers_auto` | 171.475 ms | 136.358 ms | 98.163 KiB (100,519 bytes) |
| xdelta3 3.2.0 | `single_default` | 43.225 ms | 20.700 ms | 98.089 KiB (100,443 bytes) |

### `ten_100KB_scattered`

| Tool | Profile | Create median | Apply median | Patch size |
|---|---|---:|---:|---:|
| Floating IPS 198 | `sequential_ips_exact` | 163.893 ms | 164.683 ms | 5.938 KiB (6,080 bytes) |
| HDiffPatch 5.1.2 | `WD_s64_reference` | 68.829 ms | 29.886 ms | 1.109 KiB (1,136 bytes) |
| Viper-Patcher CLI-only 1.0.0-rc.1 | `cli_compression_3_workers_auto` | 104.449 ms | 80.783 ms | 7.636 KiB (7,819 bytes) |
| Viper-Patcher hybrid 1.0.0-rc.1 | `hybrid_defaults_compression3_workers_auto` | 202.615 ms | 185.585 ms | 7.636 KiB (7,819 bytes) |
| xdelta3 3.2.0 | `sequential_default` | 245.173 ms | 219.944 ms | 6.230 KiB (6,380 bytes) |

### `ten_100KB_unrelated`

| Tool | Profile | Create median | Apply median | Patch size |
|---|---|---:|---:|---:|
| Floating IPS 198 | `sequential_ips_exact` | 150.166 ms | 145.549 ms | 976.737 KiB (1,000,179 bytes) |
| HDiffPatch 5.1.2 | `WD_s64_reference` | 123.446 ms | 27.582 ms | 976.751 KiB (1,000,193 bytes) |
| Viper-Patcher CLI-only 1.0.0-rc.1 | `cli_compression_3_workers_auto` | 204.518 ms | 71.717 ms | 979.451 KiB (1,002,958 bytes) |
| Viper-Patcher hybrid 1.0.0-rc.1 | `hybrid_defaults_compression3_workers_auto` | 320.977 ms | 167.974 ms | 979.451 KiB (1,002,958 bytes) |
| xdelta3 3.2.0 | `sequential_default` | 351.658 ms | 208.631 ms | 980.880 KiB (1,004,421 bytes) |

### `one_50MB_scattered`

| Tool | Profile | Create median | Apply median | Patch size |
|---|---|---:|---:|---:|
| Floating IPS 198 | `single_ips_exact` | unsupported_ips_over_16MiB | unsupported_ips_over_16MiB | unsupported_ips_over_16MiB |
| HDiffPatch 5.1.2 | `WD_s64_reference` | 1005.911 ms | 51.485 ms | 41.719 KiB (42,720 bytes) |
| Viper-Patcher CLI-only 1.0.0-rc.1 | `cli_compression_3_workers_auto` | 334.731 ms | 104.551 ms | 179.655 KiB (183,967 bytes) |
| Viper-Patcher hybrid 1.0.0-rc.1 | `hybrid_defaults_compression3_workers_auto` | 458.756 ms | 202.169 ms | 179.655 KiB (183,967 bytes) |
| xdelta3 3.2.0 | `single_default` | 290.209 ms | 111.275 ms | 49.874 KiB (51,071 bytes) |

### `one_50MB_unrelated`

| Tool | Profile | Create median | Apply median | Patch size |
|---|---|---:|---:|---:|
| Floating IPS 198 | `single_ips_exact` | unsupported_ips_over_16MiB | unsupported_ips_over_16MiB | unsupported_ips_over_16MiB |
| HDiffPatch 5.1.2 | `WD_s64_reference` | 5062.827 ms | 41.689 ms | 47.684 MiB (50,000,106 bytes) |
| Viper-Patcher CLI-only 1.0.0-rc.1 | `cli_compression_3_workers_auto` | 739.091 ms | 99.848 ms | 47.688 MiB (50,004,975 bytes) |
| Viper-Patcher hybrid 1.0.0-rc.1 | `hybrid_defaults_compression3_workers_auto` | 857.037 ms | 175.551 ms | 47.688 MiB (50,004,975 bytes) |
| xdelta3 3.2.0 | `single_default` | 12560.326 ms | 128.248 ms | 47.687 MiB (50,003,637 bytes) |

### `one_500MB_scattered`

| Tool | Profile | Create median | Apply median | Patch size |
|---|---|---:|---:|---:|
| Floating IPS 198 | `single_ips_exact` | unsupported_ips_over_16MiB | unsupported_ips_over_16MiB | unsupported_ips_over_16MiB |
| HDiffPatch 5.1.2 | `WD_s64_reference` | 11108.184 ms | 359.899 ms | 380.408 KiB (389,538 bytes) |
| Viper-Patcher CLI-only 1.0.0-rc.1 | `cli_compression_3_workers_auto` | 1857.603 ms | 2300.134 ms | 1.616 MiB (1,694,778 bytes) |
| Viper-Patcher hybrid 1.0.0-rc.1 | `hybrid_defaults_compression3_workers_auto` | 1915.930 ms | 1549.893 ms | 1.616 MiB (1,694,778 bytes) |
| xdelta3 3.2.0 | `single_default` | 2688.019 ms | 860.062 ms | 493.271 KiB (505,110 bytes) |

### `one_500MB_unrelated`

| Tool | Profile | Create median | Apply median | Patch size |
|---|---|---:|---:|---:|
| Floating IPS 198 | `single_ips_exact` | unsupported_ips_over_16MiB | unsupported_ips_over_16MiB | unsupported_ips_over_16MiB |
| HDiffPatch 5.1.2 | `WD_s64_reference` | 31777.584 ms | 367.315 ms | 476.837 MiB (500,000,111 bytes) |
| Viper-Patcher CLI-only 1.0.0-rc.1 | `cli_compression_3_workers_auto` | 5398.076 ms | 390.145 ms | 476.861 MiB (500,025,239 bytes) |
| Viper-Patcher hybrid 1.0.0-rc.1 | `hybrid_defaults_compression3_workers_auto` | 5061.324 ms | 436.736 ms | 476.861 MiB (500,025,239 bytes) |
| xdelta3 3.2.0 | `single_default` | 253000.081 ms | 1539.439 ms | 476.867 MiB (500,031,392 bytes) |

## Viper tuning matrix

### `one_100KB_scattered`

| Tool | Profile | Create median | Apply median | Patch size |
|---|---|---:|---:|---:|
| Viper-Patcher CLI-only 1.0.0-rc.1 | `cli_compression_1_workers_1` | 59.540 ms | 37.596 ms | 1.087 KiB (1,113 bytes) |
| Viper-Patcher CLI-only 1.0.0-rc.1 | `cli_compression_1_workers_auto` | 64.964 ms | 37.066 ms | 1.087 KiB (1,113 bytes) |
| Viper-Patcher CLI-only 1.0.0-rc.1 | `cli_compression_3_workers_1` | 60.026 ms | 37.170 ms | 1,006 bytes |
| Viper-Patcher CLI-only 1.0.0-rc.1 | `cli_compression_3_workers_auto` | 66.170 ms | 37.079 ms | 1,006 bytes |
| Viper-Patcher CLI-only 1.0.0-rc.1 | `cli_compression_9_workers_1` | 62.610 ms | 37.712 ms | 1,006 bytes |
| Viper-Patcher CLI-only 1.0.0-rc.1 | `cli_compression_9_workers_auto` | 65.337 ms | 37.572 ms | 1,006 bytes |
| Viper-Patcher hybrid 1.0.0-rc.1 | `hybrid_defaults_compression3_workers_auto` | 154.805 ms | 133.081 ms | 1,006 bytes |

### `one_100KB_unrelated`

| Tool | Profile | Create median | Apply median | Patch size |
|---|---|---:|---:|---:|
| Viper-Patcher CLI-only 1.0.0-rc.1 | `cli_compression_1_workers_1` | 78.418 ms | 37.580 ms | 98.163 KiB (100,519 bytes) |
| Viper-Patcher CLI-only 1.0.0-rc.1 | `cli_compression_1_workers_auto` | 78.376 ms | 38.742 ms | 98.163 KiB (100,519 bytes) |
| Viper-Patcher CLI-only 1.0.0-rc.1 | `cli_compression_3_workers_1` | 75.045 ms | 38.947 ms | 98.163 KiB (100,519 bytes) |
| Viper-Patcher CLI-only 1.0.0-rc.1 | `cli_compression_3_workers_auto` | 78.042 ms | 38.276 ms | 98.163 KiB (100,519 bytes) |
| Viper-Patcher CLI-only 1.0.0-rc.1 | `cli_compression_9_workers_1` | 84.032 ms | 38.013 ms | 98.163 KiB (100,519 bytes) |
| Viper-Patcher CLI-only 1.0.0-rc.1 | `cli_compression_9_workers_auto` | 94.552 ms | 39.922 ms | 98.163 KiB (100,519 bytes) |
| Viper-Patcher hybrid 1.0.0-rc.1 | `hybrid_defaults_compression3_workers_auto` | 171.475 ms | 136.358 ms | 98.163 KiB (100,519 bytes) |

### `ten_100KB_scattered`

| Tool | Profile | Create median | Apply median | Patch size |
|---|---|---:|---:|---:|
| Viper-Patcher CLI-only 1.0.0-rc.1 | `cli_compression_1_workers_1` | 122.004 ms | 128.812 ms | 8.775 KiB (8,986 bytes) |
| Viper-Patcher CLI-only 1.0.0-rc.1 | `cli_compression_1_workers_auto` | 114.121 ms | 82.597 ms | 8.775 KiB (8,986 bytes) |
| Viper-Patcher CLI-only 1.0.0-rc.1 | `cli_compression_3_workers_1` | 119.272 ms | 136.407 ms | 7.636 KiB (7,819 bytes) |
| Viper-Patcher CLI-only 1.0.0-rc.1 | `cli_compression_3_workers_auto` | 104.449 ms | 80.783 ms | 7.636 KiB (7,819 bytes) |
| Viper-Patcher CLI-only 1.0.0-rc.1 | `cli_compression_9_workers_1` | 125.738 ms | 129.343 ms | 7.639 KiB (7,822 bytes) |
| Viper-Patcher CLI-only 1.0.0-rc.1 | `cli_compression_9_workers_auto` | 109.518 ms | 76.515 ms | 7.639 KiB (7,822 bytes) |
| Viper-Patcher hybrid 1.0.0-rc.1 | `hybrid_defaults_compression3_workers_auto` | 202.615 ms | 185.585 ms | 7.636 KiB (7,819 bytes) |

### `ten_100KB_unrelated`

| Tool | Profile | Create median | Apply median | Patch size |
|---|---|---:|---:|---:|
| Viper-Patcher CLI-only 1.0.0-rc.1 | `cli_compression_1_workers_1` | 221.644 ms | 119.547 ms | 979.451 KiB (1,002,958 bytes) |
| Viper-Patcher CLI-only 1.0.0-rc.1 | `cli_compression_1_workers_auto` | 209.849 ms | 71.239 ms | 979.451 KiB (1,002,958 bytes) |
| Viper-Patcher CLI-only 1.0.0-rc.1 | `cli_compression_3_workers_1` | 223.618 ms | 120.133 ms | 979.451 KiB (1,002,958 bytes) |
| Viper-Patcher CLI-only 1.0.0-rc.1 | `cli_compression_3_workers_auto` | 204.518 ms | 71.717 ms | 979.451 KiB (1,002,958 bytes) |
| Viper-Patcher CLI-only 1.0.0-rc.1 | `cli_compression_9_workers_1` | 213.385 ms | 119.802 ms | 979.451 KiB (1,002,958 bytes) |
| Viper-Patcher CLI-only 1.0.0-rc.1 | `cli_compression_9_workers_auto` | 205.367 ms | 72.804 ms | 979.451 KiB (1,002,958 bytes) |
| Viper-Patcher hybrid 1.0.0-rc.1 | `hybrid_defaults_compression3_workers_auto` | 320.977 ms | 167.974 ms | 979.451 KiB (1,002,958 bytes) |

### `one_50MB_scattered`

| Tool | Profile | Create median | Apply median | Patch size |
|---|---|---:|---:|---:|
| Viper-Patcher CLI-only 1.0.0-rc.1 | `cli_compression_1_workers_1` | 501.247 ms | 107.911 ms | 250.301 KiB (256,308 bytes) |
| Viper-Patcher CLI-only 1.0.0-rc.1 | `cli_compression_1_workers_auto` | 326.418 ms | 113.937 ms | 250.301 KiB (256,308 bytes) |
| Viper-Patcher CLI-only 1.0.0-rc.1 | `cli_compression_3_workers_1` | 498.338 ms | 107.547 ms | 179.655 KiB (183,967 bytes) |
| Viper-Patcher CLI-only 1.0.0-rc.1 | `cli_compression_3_workers_auto` | 334.731 ms | 104.551 ms | 179.655 KiB (183,967 bytes) |
| Viper-Patcher CLI-only 1.0.0-rc.1 | `cli_compression_9_workers_1` | 528.471 ms | 107.482 ms | 179.644 KiB (183,955 bytes) |
| Viper-Patcher CLI-only 1.0.0-rc.1 | `cli_compression_9_workers_auto` | 365.369 ms | 121.001 ms | 179.644 KiB (183,955 bytes) |
| Viper-Patcher hybrid 1.0.0-rc.1 | `hybrid_defaults_compression3_workers_auto` | 458.756 ms | 202.169 ms | 179.655 KiB (183,967 bytes) |

### `one_50MB_unrelated`

| Tool | Profile | Create median | Apply median | Patch size |
|---|---|---:|---:|---:|
| Viper-Patcher CLI-only 1.0.0-rc.1 | `cli_compression_1_workers_1` | 992.339 ms | 72.021 ms | 47.688 MiB (50,004,975 bytes) |
| Viper-Patcher CLI-only 1.0.0-rc.1 | `cli_compression_1_workers_auto` | 708.581 ms | 86.518 ms | 47.688 MiB (50,004,975 bytes) |
| Viper-Patcher CLI-only 1.0.0-rc.1 | `cli_compression_3_workers_1` | 1005.997 ms | 71.789 ms | 47.688 MiB (50,004,975 bytes) |
| Viper-Patcher CLI-only 1.0.0-rc.1 | `cli_compression_3_workers_auto` | 739.091 ms | 99.848 ms | 47.688 MiB (50,004,975 bytes) |
| Viper-Patcher CLI-only 1.0.0-rc.1 | `cli_compression_9_workers_1` | 1027.777 ms | 70.969 ms | 47.688 MiB (50,004,975 bytes) |
| Viper-Patcher CLI-only 1.0.0-rc.1 | `cli_compression_9_workers_auto` | 713.439 ms | 95.006 ms | 47.688 MiB (50,004,975 bytes) |
| Viper-Patcher hybrid 1.0.0-rc.1 | `hybrid_defaults_compression3_workers_auto` | 857.037 ms | 175.551 ms | 47.688 MiB (50,004,975 bytes) |

### `one_500MB_scattered`

| Tool | Profile | Create median | Apply median | Patch size |
|---|---|---:|---:|---:|
| Viper-Patcher CLI-only 1.0.0-rc.1 | `cli_compression_1_workers_1` | 4073.602 ms | 988.890 ms | 2.395 MiB (2,511,798 bytes) |
| Viper-Patcher CLI-only 1.0.0-rc.1 | `cli_compression_1_workers_auto` | 1839.058 ms | 1409.600 ms | 2.395 MiB (2,511,798 bytes) |
| Viper-Patcher CLI-only 1.0.0-rc.1 | `cli_compression_3_workers_1` | 4069.875 ms | 989.460 ms | 1.616 MiB (1,694,778 bytes) |
| Viper-Patcher CLI-only 1.0.0-rc.1 | `cli_compression_3_workers_auto` | 1857.603 ms | 2300.134 ms | 1.616 MiB (1,694,778 bytes) |
| Viper-Patcher CLI-only 1.0.0-rc.1 | `cli_compression_9_workers_1` | 4372.139 ms | 999.140 ms | 1.616 MiB (1,694,260 bytes) |
| Viper-Patcher CLI-only 1.0.0-rc.1 | `cli_compression_9_workers_auto` | 1848.111 ms | 1370.798 ms | 1.616 MiB (1,694,260 bytes) |
| Viper-Patcher hybrid 1.0.0-rc.1 | `hybrid_defaults_compression3_workers_auto` | 1915.930 ms | 1549.893 ms | 1.616 MiB (1,694,778 bytes) |

### `one_500MB_unrelated`

| Tool | Profile | Create median | Apply median | Patch size |
|---|---|---:|---:|---:|
| Viper-Patcher CLI-only 1.0.0-rc.1 | `cli_compression_1_workers_1` | 7549.822 ms | 398.722 ms | 476.861 MiB (500,025,239 bytes) |
| Viper-Patcher CLI-only 1.0.0-rc.1 | `cli_compression_1_workers_auto` | 4633.423 ms | 274.623 ms | 476.861 MiB (500,025,239 bytes) |
| Viper-Patcher CLI-only 1.0.0-rc.1 | `cli_compression_3_workers_1` | 7650.923 ms | 392.905 ms | 476.861 MiB (500,025,239 bytes) |
| Viper-Patcher CLI-only 1.0.0-rc.1 | `cli_compression_3_workers_auto` | 5398.076 ms | 390.145 ms | 476.861 MiB (500,025,239 bytes) |
| Viper-Patcher CLI-only 1.0.0-rc.1 | `cli_compression_9_workers_1` | 7913.654 ms | 393.986 ms | 476.861 MiB (500,025,239 bytes) |
| Viper-Patcher CLI-only 1.0.0-rc.1 | `cli_compression_9_workers_auto` | 5468.871 ms | 406.868 ms | 476.861 MiB (500,025,239 bytes) |
| Viper-Patcher hybrid 1.0.0-rc.1 | `hybrid_defaults_compression3_workers_auto` | 5061.324 ms | 436.736 ms | 476.861 MiB (500,025,239 bytes) |

## Files

- `results.csv`: every repetition
- `summary.csv`: complete aggregated matrix
- `fair-comparison.csv`: reference-only view
- `system.json` and `tools-lock.json`: hardware, parameters, assets, architectures and hashes
- `expected-hashes.json`: expected dataset hashes

Synthetic inputs are deterministic high-entropy data. They do not replace benchmarks on real executables, archives, databases or game assets.
