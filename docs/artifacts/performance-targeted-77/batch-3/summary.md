# Gateway allocation baseline

Status: passed. Runtime source: f603cf216408452b71f932a2e527ddcbddbe9043. See report.json for environment, commands and fixture settings.

Median of per-repeat metrics; peak memory is the maximum sampled absolute value across repeats.

| Case | KiB/request | allocations/request | latency P50/P95/P99 ms | peak heap MiB | peak RSS MiB |
| --- | ---: | ---: | --- | ---: | ---: |
| chat-short | 143.3 | 2852 | 4.36 / 7.40 / 7.40 | 16.2 | 67.1 |
| responses-short | 174.8 | 3264 | 5.02 / 8.10 / 8.10 | 16.4 | 68.8 |
| tools32 | 610.7 | 10260 | 24.12 / 27.53 / 27.53 | 15.9 | 67.2 |
| candidates4 | 256.3 | 5682 | 9.51 / 11.12 / 11.12 | 14.5 | 68.2 |
| history256k | 11218.6 | 34431 | 54.15 / 77.85 / 77.85 | 13.9 | 67.7 |
| burst4 | 145.0 | 2861 | 6.83 / 10.52 / 10.52 | 15.6 | 68.6 |
| stream256 | 7128.9 | 98053 | 83.67 / 112.80 / 112.80 | 9.1 | 61.9 |
| stream256-cross | 20935.4 | 137488 | 149.69 / 191.67 / 191.67 | 10.7 | 66.2 |
| image2 | 67863.7 | 5183 | 177.61 / 249.46 / 249.46 | 37.6 | 92.6 |
