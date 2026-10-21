# Gateway allocation baseline

Status: passed. Runtime source: de9f314874cc969b9187131c55228a7e8b53994d. See report.json for environment, commands and fixture settings.

Median of per-repeat metrics; peak memory is the maximum sampled absolute value across repeats.

| Case | KiB/request | allocations/request | latency P50/P95/P99 ms | peak heap MiB | peak RSS MiB |
| --- | ---: | ---: | --- | ---: | ---: |
| chat-short | 140.5 | 2642 | 6.21 / 7.41 / 9.42 | 16.2 | 68.0 |
| candidates4 | 249.0 | 5105 | 9.50 / 11.65 / 12.62 | 13.6 | 67.8 |
| burst4 | 143.6 | 2638 | 6.18 / 8.50 / 9.85 | 13.8 | 67.2 |
| stream256-cross | 10688.7 | 132204 | 80.43 / 149.37 / 163.42 | 9.9 | 65.4 |
| image2 | 21917.2 | 4776 | 86.23 / 138.35 / 138.35 | 33.5 | 92.8 |
| history256k | 10530.8 | 31973 | 51.40 / 68.34 / 68.40 | 13.8 | 67.5 |
