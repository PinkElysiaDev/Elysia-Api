# Gateway allocation baseline

Status: passed. Runtime source: de9f314874cc969b9187131c55228a7e8b53994d. See report.json for environment, commands and fixture settings.

Median of per-repeat metrics; peak memory is the maximum sampled absolute value across repeats.

| Case | KiB/request | allocations/request | latency P50/P95/P99 ms | peak heap MiB | peak RSS MiB |
| --- | ---: | ---: | --- | ---: | ---: |
| chat-short | 142.6 | 2639 | 3.60 / 7.81 / 8.24 | 15.9 | 68.6 |
| candidates4 | 246.0 | 5105 | 9.40 / 11.84 / 14.54 | 14.8 | 68.9 |
| burst4 | 142.7 | 2639 | 3.88 / 8.56 / 9.11 | 13.7 | 67.8 |
| stream256-cross | 10645.6 | 132209 | 99.29 / 157.59 / 160.79 | 10.5 | 65.4 |
| image2 | 21924.5 | 4785 | 145.45 / 157.25 / 157.25 | 35.6 | 94.7 |
