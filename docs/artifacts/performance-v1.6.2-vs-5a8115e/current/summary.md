# Gateway allocation baseline

Status: passed. Runtime source: 5a8115e7008e1c9f6035629bbeb58ad119bc2a43. See report.json for environment, commands and fixture settings.

Median of per-repeat metrics; peak memory is the maximum sampled absolute value across repeats.

| Case | KiB/request | allocations/request | latency P50/P95/P99 ms | peak heap MiB | peak RSS MiB |
| --- | ---: | ---: | --- | ---: | ---: |
| chat-short | 198.4 | 3462 | 6.75 / 7.94 / 9.50 | 15.9 | 67.6 |
| anthropic-short | 261.0 | 4125 | 6.46 / 9.54 / 12.77 | 17.0 | 68.8 |
| responses-short | 226.3 | 3878 | 7.88 / 9.23 / 10.11 | 16.3 | 69.1 |
| gemini-short | 253.3 | 3942 | 7.22 / 8.85 / 11.16 | 17.5 | 67.9 |
| chat-anthropic | 223.8 | 3445 | 6.93 / 8.14 / 11.83 | 16.7 | 69.7 |
| chat-responses | 198.2 | 3010 | 6.23 / 7.74 / 8.52 | 16.5 | 69.7 |
| prefix32k | 1917.2 | 4023 | 11.66 / 15.37 / 17.06 | 11.1 | 68.4 |
| history256k | 12755.8 | 35365 | 65.29 / 79.95 / 83.90 | 16.1 | 71.6 |
| tools32 | 714.2 | 11188 | 25.25 / 29.05 / 30.90 | 14.7 | 66.7 |
| candidates4 | 408.8 | 7351 | 10.90 / 13.72 / 15.62 | 15.9 | 69.1 |
| reload-chat | 258.2 | 3868 | 6.72 / 6.72 / 6.72 | 16.5 | 69.7 |
| burst4 | 203.9 | 3458 | 3.47 / 8.26 / 10.23 | 15.6 | 68.1 |
| burst4-body | 214.0 | 3670 | 6.11 / 9.77 / 12.82 | 17.4 | 70.2 |
| stream16 | 794.3 | 11019 | 8.52 / 17.58 / 18.90 | 14.5 | 67.9 |
| stream256 | 9619.3 | 113415 | 91.28 / 123.74 / 128.57 | 10.0 | 65.5 |
| stream1024 | 36447.3 | 440964 | 262.91 / 418.54 / 454.31 | 10.9 | 65.6 |
| stream256-cross | 23650.3 | 157122 | 160.94 / 206.40 / 214.49 | 11.1 | 69.6 |
| stream256-burst4 | 9028.3 | 113314 | 18.77 / 55.67 / 65.02 | 16.3 | 71.2 |
| image2 | 105690.7 | 6242 | 225.44 / 307.21 / 307.21 | 59.8 | 115.6 |
| image2-media | 131824.0 | 6796 | 256.70 / 487.62 / 487.62 | 60.6 | 117.7 |
