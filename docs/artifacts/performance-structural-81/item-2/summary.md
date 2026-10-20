# Gateway allocation baseline

Status: passed. Runtime source: a433eb87fe2922dee8820a00f9904c81e84f0c5b. See report.json for environment, commands and fixture settings.

Median of per-repeat metrics; peak memory is the maximum sampled absolute value across repeats.

| Case | KiB/request | allocations/request | latency P50/P95/P99 ms | peak heap MiB | peak RSS MiB |
| --- | ---: | ---: | --- | ---: | ---: |
| chat-short | 144.5 | 2851 | 3.15 / 4.30 / 4.30 | 16.2 | 67.6 |
| candidates4 | 255.0 | 5673 | 5.51 / 8.39 / 8.39 | 15.3 | 68.4 |
| history256k | 10419.8 | 34409 | 33.11 / 40.86 / 40.86 | 12.7 | 67.1 |
| burst4 | 143.1 | 2849 | 3.44 / 5.09 / 5.09 | 15.9 | 67.5 |
| stream256-cross | 20940.2 | 137465 | 74.26 / 101.05 / 101.05 | 10.3 | 66.1 |
| image2 | 21838.6 | 5074 | 97.04 / 115.45 / 115.45 | 35.5 | 91.6 |
| image2-media | 47661.1 | 5630 | 202.42 / 275.49 / 275.49 | 38.2 | 97.1 |
