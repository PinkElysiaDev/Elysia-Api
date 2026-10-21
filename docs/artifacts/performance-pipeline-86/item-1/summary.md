# Gateway allocation baseline

Status: passed. Runtime source: 8099841f181c3d9e3a0a7047f447a63ec35cadd7. See report.json for environment, commands and fixture settings.

Median of per-repeat metrics; peak memory is the maximum sampled absolute value across repeats.

| Case | KiB/request | allocations/request | latency P50/P95/P99 ms | peak heap MiB | peak RSS MiB |
| --- | ---: | ---: | --- | ---: | ---: |
| chat-short | 142.6 | 2851 | 6.14 / 7.13 / 7.46 | 15.9 | 68.2 |
| candidates4 | 261.2 | 5693 | 9.99 / 11.73 / 13.63 | 14.8 | 68.0 |
| burst4 | 142.6 | 2848 | 4.87 / 9.50 / 9.63 | 15.5 | 68.5 |
| stream256-cross | 20823.5 | 137473 | 137.11 / 198.82 / 202.49 | 10.8 | 67.0 |
| image2 | 21835.3 | 5092 | 130.59 / 155.41 / 155.41 | 31.3 | 89.7 |
