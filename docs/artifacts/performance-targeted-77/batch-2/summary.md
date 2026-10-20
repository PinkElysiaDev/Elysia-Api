# Gateway allocation baseline

Status: passed. Runtime source: d0e7bc0af42d8c5119ffefb626cb271de2bdd608. See report.json for environment, commands and fixture settings.

Median of per-repeat metrics; peak memory is the maximum sampled absolute value across repeats.

| Case | KiB/request | allocations/request | latency P50/P95/P99 ms | peak heap MiB | peak RSS MiB |
| --- | ---: | ---: | --- | ---: | ---: |
| chat-short | 147.1 | 2852 | 2.30 / 7.10 / 7.10 | 16.0 | 67.7 |
| responses-short | 168.3 | 3265 | 6.91 / 8.46 / 8.46 | 15.9 | 69.7 |
| tools32 | 616.0 | 10263 | 25.06 / 31.57 / 31.57 | 16.9 | 68.0 |
| candidates4 | 256.5 | 5682 | 9.94 / 12.36 / 12.36 | 15.2 | 68.2 |
| history256k | 11255.1 | 34423 | 19.84 / 76.59 / 76.59 | 13.7 | 67.6 |
| burst4 | 144.6 | 2855 | 4.22 / 6.94 / 6.94 | 15.3 | 68.6 |
| stream256 | 8036.5 | 100126 | 80.94 / 112.88 / 112.88 | 9.2 | 62.6 |
| stream256-cross | 22342.2 | 144028 | 147.73 / 205.22 / 205.22 | 10.8 | 66.3 |
| image2 | 67968.6 | 5204 | 251.99 / 266.06 / 266.06 | 37.0 | 93.9 |
