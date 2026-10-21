# Gateway allocation baseline

Status: passed. Runtime source: de9f314874cc969b9187131c55228a7e8b53994d. See report.json for environment, commands and fixture settings.

Median of per-repeat metrics; peak memory is the maximum sampled absolute value across repeats.

| Case | KiB/request | allocations/request | latency P50/P95/P99 ms | peak heap MiB | peak RSS MiB |
| --- | ---: | ---: | --- | ---: | ---: |
| chat-short | 141.1 | 2649 | 3.16 / 6.49 / 6.86 | 15.8 | 67.9 |
| candidates4 | 249.1 | 5114 | 9.71 / 12.01 / 12.91 | 15.1 | 68.9 |
| burst4 | 145.4 | 2648 | 4.86 / 8.17 / 9.20 | 14.8 | 68.6 |
| stream256-cross | 10669.6 | 132188 | 104.60 / 164.80 / 170.74 | 10.4 | 65.7 |
| image2 | 21794.1 | 4893 | 112.75 / 139.86 / 139.86 | 37.7 | 94.0 |
| history256k | 10540.8 | 33533 | 59.63 / 75.17 / 76.32 | 13.2 | 67.5 |
