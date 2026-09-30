# Login character trace production and review

[Documentation](../../docs/README.en.md) · [简体中文](README.md) · **English**

This directory is for development-time asset production. The page consumes the bundled per-frame traces; it does not install Python, OpenCV or vision models, or call an external model API.

## Prerequisites and reproduction

Run from the repository root:

```sh
python -m pip install --target .tmp-dev/vision-python -r scripts/login-trace/requirements.txt
python scripts/login-trace/extract.py
node scripts/login-trace/verify.mjs
```

Use Python 3.12. Dependencies install into the ignored `.tmp-dev/vision-python`, leaving global Python unchanged. Ordinary frontend builds run only the Node verifier, not extraction.

Inspect the first frame without replacing release assets:

```sh
python scripts/login-trace/extract.py --limit 1
```

Full extraction reads presentation timestamps from the MP4 decoder, not timers or decode order. The fixed source is 1920 × 1080, 60fps, 960 frames and 16 seconds. Non-monotonic timestamps, decode failures and changing dimensions fail the run.

## Semantic annotations and corrections

After regeneration, use the viewer and `report.json` to review affected frames and their neighbors. Automated metrics do not replace manual semantic acceptance.

- `annotations.json` defines four semantic groups: hair/accessories, face, hands, and clothing/remaining decorations.
- `sourceRegions` and `sourceExclusions` use source-video pixel coordinates; `targetRegions` use the 760 × 808 target line-art coordinates.
- Reference-frame features and optical flow locate regions. `trackWith` lets small attached regions reuse stable subject motion instead of tracking background features independently.
- Regions constrain candidates. Actual strokes come from each frame's dark-line centerlines and boundaries, never from region polygons or cropped image edges.
- Segments have cross-frame identifiers but independent point lists in each frame. Closed eyes and occlusion are represented by absent segments, without connecting nonexistent strokes.
- Tighten source regions or add source-coordinate `corrections` with `startFrame`, `endFrame`, `polygon`, `exclude` and `group`. After extraction, inspect affected and adjacent frames.
- Exclude background crystals, halos and light. Inspect accessory tips, hair ends, fingers, blinks and the loop seam. Source and target poses differ, so particles map by semantic part rather than forcing a whole-outline deformation.

## Review outputs

A full run generates `.tmp-dev/login-trace-review`:

- `overlay.mp4`: complete stroke-overlay playback at source resolution/frame rate.
- `index.html`: a local browser viewer with exact access to all 960 frames, left/right stepping, source/overlay/line-only views and 100%/200% detail. Traces are embedded; no server is required. It reads the repository's original video, independent of browser support for OpenCV's MP4 encoding.
- `contact-000.jpg` and subsequent files: 16 consecutive frames per sheet, covering all 960 frames.
- `frame-XXXX.png`, `overlay-XXXX.png`, `lines-XXXX.png`: marked keyframes as source, overlay and line-only images for zoomed review.
- `report.json`: per-frame PTS, segment/vertex counts, group coverage, tracked reference-feature counts and exported-polyline deviation from the extracted skeleton.

**Metric definition:** `maxCenterlineDeviationPx` measures exported polylines against that frame's extracted skeleton. It is not semantic accuracy against independently annotated pixel ground truth. Automated full-video checks plus visual keyframe review do not prove that all 960 frames were manually verified below 2px. The 2px target applies to major clear structural lines. Correct false positives, omissions and misalignment in annotations; do not hide them with thicker strokes.

Identifier matching uses equidistant stroke samples, local optical-flow prediction and bidirectional shape residuals, followed by global greedy one-to-one assignment. Splits, merges, occlusion and ambiguous matches create new IDs instead of forcing adjacent hair/clothing lines into one identity. To adjust tracking without re-extracting geometry, run `python scripts/login-trace/retrack.py`; it refreshes release hashes and the viewer and writes `tracking-report.json`. Its residual threshold does not replace the 2px semantic target.

Run `python scripts/login-trace/preview.py` to rebuild only the viewer. Review images/video stay in ignored `.tmp-dev` and add no web downloads. Production scripts, correction regions and complete traces are delivered files.

## Release assets and format

Full extraction updates these files; commit them together:

- `packages/webui/public/assets/elysia-character-trace.json`: source/target/annotation hashes, PTS index, groups and target strokes.
- `packages/webui/public/assets/elysia-character-trace.bin.gz`: gzip-compressed per-frame segments.
- `packages/webui/src/lib/login-media-version.json`: browser cache versions bound to video, trace and target art.

Binary fields are little-endian. Each frame begins with `uint32 pathCount`. Each segment contains `uint32 id`, `uint8 group`, `uint8 closed`, `uint16 pointCount`, followed by signed 16-bit XY pixel deltas. The first point is relative to the origin; subsequent points are relative to the previous point. The browser normalizes after decoding. Absent segments are invisible. Frame offsets refer to decompressed bytes.

Both compressed and decompressed content have SHA-256 hashes. Some static servers add `Content-Encoding: gzip` to `.gz`, so Fetch may return decompressed bytes. The loader checks the header to distinguish raw gzip from decoded content and verifies the corresponding hash, avoiding double decompression or a false version mismatch.

The Node verifier checks all asset hashes, all 960 frames, monotonic PTS, bounds, groups and path identifiers. Replacing the video/target or editing annotations without exporting again must fail the build.

## Frontend verification

These are Windows PowerShell commands. On macOS/Linux, replace `npm.cmd` with `npm` and omit the `$env:` line to use Playwright Chromium.

```powershell
npm.cmd run build:webui
npm.cmd run lint --workspace @root/webui
$env:PLAYWRIGHT_CHANNEL = 'msedge'
npm.cmd run test:e2e --workspace @root/webui -- --workers=1
```

Platforms without Edge can use installed Playwright Chromium without `PLAYWRIGHT_CHANNEL`. End-to-end tests use mocked admin responses and test tokens, without reading real configuration.

The transition selects traces by video presentation time and uses a separate four-second timeline for particles/text. Video, strokes and particles share one WebGL2 canvas. Pausing, backgrounding, missing assets or rendering failure never blocks an already authenticated login. Development-only `data-media-time`, `data-elapsed` and `data-paths` attributes support sync inspection; release builds omit them.

Frame callbacks prefer an immutable `VideoFrame`, use its own PTS to select traces, upload that same frame to the GPU and release it immediately. Main-thread stalls can make a callback's old `mediaTime` lag behind the actual frame read from the video; this was reproduced with a 220ms stall. Do not pair old metadata with a newer video image. Without `VideoFrame`, only on-time callbacks are accepted; repeated lateness completes login without showing misaligned strokes.

## Browser capture and performance reproduction

In another terminal, run `npm run dev --workspace @root/webui -- --port 5274 --strictPort`. Then run the following in Windows PowerShell; other platforms omit the `$env:` line:

```powershell
$env:PLAYWRIGHT_CHANNEL = 'msedge'
node scripts/login-trace/capture.mjs
node scripts/login-trace/capture.mjs --measure
```

Outputs in `.tmp-dev/login-browser-review` include light/dark desktop and touch-mobile login pages, four transition stages, handoff captures and JSON reports. Mobile playback starts at 15 seconds to check the loop seam; DPR 1/2 are also covered. Performance is measured separately without transition screenshots, recording actual canvas callback frequency and intervals. This does not guarantee GPU frame rates on physical mobile devices; verify 60/30fps on target hardware. Defaults are 1600 desktop and 650 mobile particles, with canvas DPR capped at 2/1.5 and composite draws at 60/30Hz to avoid excess work on high-refresh displays. Video callbacks run independently; each composite uses the latest presented frame's exact trace, without sparse-keyframe interpolation.

## Troubleshooting

| Symptom | Action |
| --- | --- |
| Python imports fail | Use Python 3.12, install into `.tmp-dev/vision-python` and run from the repository root |
| Decode, PTS or dimension checks fail | Check source integrity and annotation dimensions; keep timestamp validation enabled |
| Build hash check fails | Perform a full extraction and update JSON, binary and version files together; `--limit` is not a release export |
| Incorrect or misaligned strokes | Correct semantic regions, re-extract and inspect affected/adjacent frames; do not conceal errors with thicker lines |
| Browser capture cannot connect | Check the port 5274 dev server and browser channel; override the URL with `LOGIN_REVIEW_URL` if needed |
