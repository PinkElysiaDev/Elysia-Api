# Gateway allocation baseline

Status: passed. Runtime source: caa441ec5bafa3a457ea0c082e526e220dfdf4b9. See report.json for environment, commands and fixture settings.

Median of per-repeat metrics; peak memory is the maximum sampled absolute value across repeats.

| Case | KiB/request | allocations/request | latency P50/P95/P99 ms | peak heap MiB | peak RSS MiB |
| --- | ---: | ---: | --- | ---: | ---: |
| chat-short | 141.5 | 2639 | 2.42 / 6.92 / 8.32 | 15.8 | 67.5 |
| candidates4 | 255.7 | 5105 | 8.86 / 12.03 / 12.11 | 14.6 | 68.6 |
| burst4 | 145.6 | 2646 | 3.32 / 6.80 / 6.83 | 13.3 | 67.9 |
| stream256-cross | 20417.4 | 133037 | 119.48 / 184.24 / 191.66 | 10.6 | 66.9 |
| image2 | 21677.3 | 4825 | 83.82 / 92.46 / 92.46 | 35.2 | 92.9 |
