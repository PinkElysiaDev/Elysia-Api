# Gateway allocation baseline

Status: passed. Runtime source: 281a3d7537874e66d56948a6a8cd9005596fc978. See report.json for environment, commands and fixture settings.

Median of per-repeat metrics; peak memory is the maximum sampled absolute value across repeats.

| Case | KiB/request | allocations/request | latency P50/P95/P99 ms | peak heap MiB | peak RSS MiB |
| --- | ---: | ---: | --- | ---: | ---: |
| chat-short | 139.5 | 2641 | 5.85 / 7.05 / 9.96 | 15.5 | 66.9 |
| candidates4 | 254.4 | 5106 | 9.26 / 11.02 / 11.28 | 13.7 | 67.7 |
| burst4 | 139.5 | 2638 | 2.15 / 4.48 / 4.91 | 14.3 | 68.2 |
| stream256-cross | 10682.7 | 132214 | 108.97 / 163.68 / 170.49 | 10.0 | 64.2 |
| image2 | 21795.7 | 4898 | 129.04 / 146.70 / 146.70 | 37.6 | 94.2 |
