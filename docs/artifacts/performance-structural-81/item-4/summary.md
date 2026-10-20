# Gateway allocation baseline

Status: passed. Runtime source: 669ef17b1f402c785d0a4ec6b847ad6b670acf8a. See report.json for environment, commands and fixture settings.

Median of per-repeat metrics; peak memory is the maximum sampled absolute value across repeats.

| Case | KiB/request | allocations/request | latency P50/P95/P99 ms | peak heap MiB | peak RSS MiB |
| --- | ---: | ---: | --- | ---: | ---: |
| chat-short | 145.1 | 2862 | 3.30 / 4.18 / 4.18 | 15.0 | 67.3 |
| candidates4 | 258.4 | 5691 | 5.82 / 7.52 / 7.52 | 14.3 | 67.2 |
| history256k | 10460.2 | 34406 | 33.77 / 39.32 / 39.32 | 13.5 | 67.3 |
| burst4 | 145.2 | 2860 | 2.68 / 3.96 / 3.96 | 15.4 | 69.5 |
| stream256-cross | 20794.5 | 137454 | 80.68 / 101.65 / 101.65 | 10.5 | 65.8 |
| image2 | 21876.0 | 5074 | 86.96 / 122.01 / 122.01 | 37.3 | 91.3 |
| image2-media | 47716.0 | 5668 | 260.49 / 353.27 / 353.27 | 42.6 | 99.2 |
