# Gateway allocation baseline

Status: passed. Runtime source: 647ab51d5329b5fd703a9b02dd91a1e0fad237f6. See report.json for environment, commands and fixture settings.

Median of per-repeat metrics; peak memory is the maximum sampled absolute value across repeats.

| Case | KiB/request | allocations/request | latency P50/P95/P99 ms | peak heap MiB | peak RSS MiB |
| --- | ---: | ---: | --- | ---: | ---: |
| chat-short | 140.2 | 2684 | 5.70 / 7.05 / 7.43 | 15.7 | 66.7 |
| candidates4 | 249.1 | 5181 | 8.35 / 11.64 / 12.43 | 15.4 | 67.6 |
| burst4 | 144.9 | 2689 | 6.08 / 10.38 / 11.97 | 16.5 | 69.6 |
| stream256-cross | 20755.4 | 137264 | 153.74 / 194.37 / 194.59 | 10.6 | 67.2 |
| image2 | 21836.3 | 4891 | 100.77 / 145.62 / 145.62 | 37.7 | 96.8 |
