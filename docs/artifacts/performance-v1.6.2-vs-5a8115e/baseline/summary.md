# Gateway allocation baseline

Status: passed. Runtime source: 8f50807c295f9d6f2669d79794e58c89188722b3. See report.json for environment, commands and fixture settings.

Median of per-repeat metrics; peak memory is the maximum sampled absolute value across repeats.

| Case | KiB/request | allocations/request | latency P50/P95/P99 ms | peak heap MiB | peak RSS MiB |
| --- | ---: | ---: | --- | ---: | ---: |
| chat-short | 1287.4 | 7746 | 14.68 / 21.77 / 22.34 | 5.7 | 41.3 |
| anthropic-short | 1501.3 | 9471 | 23.01 / 27.38 / 28.37 | 5.9 | 42.7 |
| responses-short | 1730.4 | 8819 | 23.97 / 27.16 / 29.16 | 6.5 | 42.4 |
| gemini-short | 1473.1 | 8902 | 18.40 / 24.41 / 26.09 | 6.0 | 41.6 |
| chat-anthropic | 1468.5 | 8181 | 9.30 / 21.69 / 24.95 | 5.9 | 43.0 |
| chat-responses | 1723.5 | 7567 | 20.55 / 26.37 / 27.69 | 6.7 | 43.5 |
| prefix32k | 3149.8 | 8564 | 32.01 / 53.74 / 55.31 | 6.7 | 43.2 |
| history256k | 15706.3 | 53589 | 172.57 / 254.17 / 283.72 | 10.7 | 47.2 |
| tools32 | 1939.3 | 19392 | 35.69 / 41.09 / 42.16 | 5.8 | 41.4 |
| candidates4 | 4469.7 | 22211 | 45.29 / 58.97 / 61.43 | 7.8 | 45.6 |
| reload-chat | 1329.3 | 8121 | 11.45 / 11.45 / 11.45 | 6.4 | 42.1 |
| burst4 | 1283.9 | 7734 | 20.79 / 46.87 / 61.62 | 6.7 | 43.1 |
| burst4-body | 1296.5 | 8050 | 21.64 / 54.88 / 79.84 | 6.6 | 43.8 |
| stream16 | 2053.4 | 16788 | 29.11 / 35.29 / 36.44 | 5.9 | 41.5 |
| stream256 | 12548.9 | 136781 | 157.35 / 195.43 / 203.01 | 6.1 | 42.0 |
| stream1024 | 46088.9 | 520475 | 508.16 / 665.41 / 695.75 | 6.8 | 44.7 |
| stream256-cross | 43971.5 | 198774 | 295.87 / 366.77 / 376.99 | 7.6 | 46.9 |
| stream256-burst4 | 12505.0 | 136620 | 31.23 / 68.67 / 85.62 | 10.9 | 49.4 |
| image2 | 125581.5 | 12704 | 556.97 / 760.41 / 760.41 | 42.7 | 76.4 |
| image2-media | 147663.0 | 13438 | 637.80 / 887.60 / 887.60 | 59.7 | 91.6 |
