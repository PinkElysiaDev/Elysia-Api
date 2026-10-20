# Gateway allocation baseline

Status: passed. Runtime source: 8e732c7a14d2c6ebaf30e6f2a6f40565883d36c9. See report.json for environment, commands and fixture settings.

Median of per-repeat metrics; peak memory is the maximum sampled absolute value across repeats.

| Case | KiB/request | allocations/request | latency P50/P95/P99 ms | peak heap MiB | peak RSS MiB |
| --- | ---: | ---: | --- | ---: | ---: |
| chat-short | 144.7 | 2856 | 6.20 / 6.89 / 6.89 | 15.6 | 66.8 |
| responses-short | 167.8 | 3270 | 6.79 / 9.62 / 9.62 | 16.2 | 67.8 |
| tools32 | 608.4 | 10258 | 12.78 / 27.48 / 27.48 | 13.3 | 67.1 |
| candidates4 | 254.8 | 5675 | 9.99 / 13.58 / 13.58 | 14.9 | 67.6 |
| history256k | 12293.0 | 34423 | 26.72 / 77.50 / 77.50 | 14.8 | 69.4 |
| burst4 | 150.2 | 2863 | 6.63 / 7.15 / 7.15 | 15.8 | 68.5 |
| stream256 | 8602.6 | 109706 | 68.50 / 118.47 / 118.47 | 9.3 | 62.0 |
| stream256-cross | 22826.3 | 153589 | 49.05 / 205.16 / 205.16 | 10.3 | 65.5 |
| image2 | 102930.3 | 5231 | 206.27 / 213.58 / 213.58 | 64.0 | 121.3 |
