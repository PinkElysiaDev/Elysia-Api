# Gateway allocation baseline

Status: passed. Runtime source: bdb02c61ec955cf83421e4fefc0e9be53b4c022c. See report.json for environment, commands and fixture settings.

Median of per-repeat metrics; peak memory is the maximum sampled absolute value across repeats.

| Case | KiB/request | allocations/request | latency P50/P95/P99 ms | peak heap MiB | peak RSS MiB |
| --- | ---: | ---: | --- | ---: | ---: |
| chat-short | 140.7 | 2650 | 5.91 / 6.97 / 7.73 | 16.0 | 67.5 |
| candidates4 | 251.3 | 5111 | 4.59 / 11.15 / 11.58 | 14.5 | 67.8 |
| burst4 | 139.4 | 2638 | 4.39 / 5.79 / 7.07 | 15.1 | 68.0 |
| stream256-cross | 10634.4 | 132208 | 107.52 / 153.10 / 161.27 | 10.2 | 64.8 |
| image2 | 21832.5 | 4841 | 143.93 / 182.98 / 182.98 | 37.3 | 92.6 |
