# Gateway allocation baseline

Status: passed. Runtime source: 8e732c7a14d2c6ebaf30e6f2a6f40565883d36c9. See report.json for environment, commands and fixture settings.

Median of per-repeat metrics; peak memory is the maximum sampled absolute value across repeats.

| Case | KiB/request | allocations/request | latency P50/P95/P99 ms | peak heap MiB | peak RSS MiB |
| --- | ---: | ---: | --- | ---: | ---: |
| chat-short | 197.1 | 3464 | 6.53 / 8.01 / 8.01 | 15.0 | 67.2 |
| responses-short | 234.0 | 3883 | 8.01 / 9.31 / 9.31 | 16.6 | 69.4 |
| tools32 | 721.9 | 11188 | 22.31 / 27.79 / 27.79 | 16.2 | 67.6 |
| candidates4 | 414.9 | 7347 | 11.84 / 13.55 / 13.55 | 15.9 | 69.6 |
| history256k | 12708.6 | 35364 | 52.21 / 71.60 / 71.60 | 15.7 | 69.2 |
| burst4 | 197.4 | 3466 | 7.39 / 11.85 / 11.85 | 17.6 | 69.3 |
| stream256 | 9637.2 | 113423 | 99.16 / 123.82 / 123.82 | 9.7 | 63.5 |
| stream256-cross | 23804.9 | 157136 | 139.83 / 204.73 / 204.73 | 10.4 | 66.3 |
| image2 | 105644.1 | 6240 | 231.30 / 319.15 / 319.15 | 58.4 | 114.9 |
