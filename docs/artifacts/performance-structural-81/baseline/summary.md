# Gateway allocation baseline

Status: passed. Runtime source: 1ddf7af9d03e3d2a1fbfe6f320ca24b1f24514cc. See report.json for environment, commands and fixture settings.

Median of per-repeat metrics; peak memory is the maximum sampled absolute value across repeats.

| Case | KiB/request | allocations/request | latency P50/P95/P99 ms | peak heap MiB | peak RSS MiB |
| --- | ---: | ---: | --- | ---: | ---: |
| chat-short | 149.9 | 2854 | 5.54 / 7.96 / 7.96 | 15.8 | 67.7 |
| candidates4 | 258.4 | 5690 | 10.15 / 13.51 / 13.51 | 14.7 | 67.9 |
| history256k | 11197.9 | 34444 | 68.83 / 79.68 / 79.68 | 14.3 | 68.7 |
| burst4 | 152.3 | 2863 | 1.56 / 3.13 / 3.13 | 16.5 | 68.6 |
| stream256-cross | 20891.9 | 137444 | 45.28 / 47.94 / 47.94 | 10.7 | 65.9 |
| image2 | 67840.5 | 5217 | 227.47 / 315.19 / 315.19 | 33.5 | 93.4 |
| image2-media | 93684.5 | 5727 | 227.88 / 444.50 / 444.50 | 39.2 | 98.8 |
