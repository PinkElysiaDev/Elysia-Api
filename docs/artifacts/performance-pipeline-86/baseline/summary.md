# Gateway allocation baseline

Status: passed. Runtime source: 8099841f181c3d9e3a0a7047f447a63ec35cadd7. See report.json for environment, commands and fixture settings.

Median of per-repeat metrics; peak memory is the maximum sampled absolute value across repeats.

| Case | KiB/request | allocations/request | latency P50/P95/P99 ms | peak heap MiB | peak RSS MiB |
| --- | ---: | ---: | --- | ---: | ---: |
| chat-short | 143.6 | 2857 | 2.63 / 6.49 / 6.50 | 15.5 | 67.3 |
| candidates4 | 254.9 | 5675 | 10.01 / 12.21 / 14.45 | 15.2 | 69.2 |
| burst4 | 145.1 | 2861 | 6.39 / 10.26 / 10.87 | 13.5 | 66.4 |
| stream256-cross | 20817.0 | 137477 | 98.42 / 194.10 / 199.87 | 10.6 | 67.1 |
| image2 | 21891.7 | 5091 | 131.33 / 170.62 / 170.62 | 33.9 | 88.6 |
