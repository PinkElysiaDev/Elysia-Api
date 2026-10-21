# Gateway allocation baseline

Status: passed. Runtime source: 917041dd02def43eaf967f2c8867a2d78fab9e04. See report.json for environment, commands and fixture settings.

Median of per-repeat metrics; peak memory is the maximum sampled absolute value across repeats.

| Case | KiB/request | allocations/request | latency P50/P95/P99 ms | peak heap MiB | peak RSS MiB |
| --- | ---: | ---: | --- | ---: | ---: |
| chat-short | 141.1 | 2652 | 5.72 / 6.63 / 7.07 | 15.6 | 67.2 |
| candidates4 | 249.8 | 5107 | 8.84 / 10.70 / 13.10 | 15.7 | 69.5 |
| burst4 | 142.2 | 2640 | 5.87 / 9.68 / 9.80 | 16.1 | 69.4 |
| stream256-cross | 20533.3 | 133021 | 76.13 / 191.23 / 194.02 | 10.7 | 67.2 |
| image2 | 21750.3 | 4823 | 90.85 / 132.85 / 132.85 | 33.4 | 93.7 |
