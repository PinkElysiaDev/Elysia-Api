# Gateway allocation baseline

Status: passed. Runtime source: 1ddf7af9d03e3d2a1fbfe6f320ca24b1f24514cc. See report.json for environment, commands and fixture settings.

Median of per-repeat metrics; peak memory is the maximum sampled absolute value across repeats.

| Case | KiB/request | allocations/request | latency P50/P95/P99 ms | peak heap MiB | peak RSS MiB |
| --- | ---: | ---: | --- | ---: | ---: |
| chat-short | 146.0 | 2853 | 3.37 / 4.06 / 4.06 | 15.4 | 66.3 |
| candidates4 | 267.5 | 5677 | 5.92 / 7.56 / 7.56 | 15.4 | 68.5 |
| history256k | 11268.4 | 34420 | 28.84 / 72.30 / 72.30 | 14.1 | 68.1 |
| burst4 | 143.1 | 2853 | 5.59 / 7.93 / 7.93 | 15.5 | 67.5 |
| stream256-cross | 20966.7 | 137491 | 128.27 / 153.15 / 153.15 | 10.8 | 66.5 |
| image2 | 38731.0 | 5119 | 82.04 / 161.51 / 161.51 | 35.4 | 88.9 |
| image2-media | 64473.0 | 5699 | 262.01 / 390.23 / 390.23 | 39.6 | 98.6 |
