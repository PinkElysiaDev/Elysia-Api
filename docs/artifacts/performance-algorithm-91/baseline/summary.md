# Gateway allocation baseline

Status: passed. Runtime source: bdb02c61ec955cf83421e4fefc0e9be53b4c022c. See report.json for environment, commands and fixture settings.

Median of per-repeat metrics; peak memory is the maximum sampled absolute value across repeats.

| Case | KiB/request | allocations/request | latency P50/P95/P99 ms | peak heap MiB | peak RSS MiB |
| --- | ---: | ---: | --- | ---: | ---: |
| chat-short | 139.1 | 2641 | 5.73 / 7.53 / 10.86 | 16.0 | 68.0 |
| candidates4 | 246.7 | 5107 | 9.71 / 11.06 / 13.59 | 14.2 | 68.1 |
| burst4 | 139.1 | 2640 | 6.11 / 9.93 / 10.10 | 15.6 | 68.0 |
| stream256-cross | 20489.4 | 133041 | 132.27 / 188.05 / 197.15 | 10.5 | 67.3 |
| image2 | 21739.5 | 4831 | 134.98 / 156.84 / 156.84 | 35.4 | 94.2 |
