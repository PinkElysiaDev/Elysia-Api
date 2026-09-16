import gzip
import hashlib
import json
import struct

from extract import ROOT, cv2, np, track_ids
from preview import write_preview


def main():
    cv2.setNumThreads(4)
    assets = ROOT / 'packages/webui/public/assets'
    manifest_path = assets / 'elysia-character-trace.json'
    manifest = json.loads(manifest_path.read_text('utf-8'))
    binary = bytearray(gzip.decompress((assets / manifest['payload']).read_bytes()))
    capture = cv2.VideoCapture(str(assets / manifest['source']['file']))
    previous_paths, previous_gray, next_id = [], None, 1
    statistics = []
    for frame_index, offset in enumerate(manifest['frameOffsets']):
        success, image = capture.read()
        if not success:
            raise RuntimeError(f'Cannot decode frame {frame_index}')
        gray = cv2.cvtColor(image, cv2.COLOR_BGR2GRAY)
        count = struct.unpack_from('<I', binary, offset)[0]
        offset += 4
        paths = []
        for _ in range(count):
            identifier, group, closed, points = struct.unpack_from('<IBBH', binary, offset)
            coordinates = np.frombuffer(binary, dtype='<i2', count=points * 2, offset=offset + 8).reshape(-1, 2).cumsum(axis=0).astype(np.float32)
            paths.append({'id': identifier, 'group': group, 'closed': bool(closed), 'points': coordinates, 'offset': offset})
            offset += 8 + points * 4
        next_id = track_ids(paths, previous_paths, previous_gray, gray, next_id)
        for path in paths:
            struct.pack_into('<I', binary, path['offset'], path['id'])
        residuals = [path['trackingErrorPx'] for path in paths if 'trackingErrorPx' in path]
        statistics.append({'frame': frame_index, 'time': manifest['frameTimes'][frame_index], 'paths': count,
                           'matched': len(residuals), 'medianResidualPx': float(np.median(residuals)) if residuals else 0,
                           'maxResidualPx': max(residuals, default=0)})
        previous_paths, previous_gray = paths, gray
        if frame_index % 60 == 0:
            print(f'Tracked {frame_index}/960: {len(residuals)}/{count} matched, residual {max(residuals, default=0):.2f}px', flush=True)
    capture.release()
    compressed = gzip.compress(bytes(binary), compresslevel=9, mtime=0)
    (assets / manifest['payload']).write_bytes(compressed)
    manifest['payloadSha256'] = hashlib.sha256(compressed).hexdigest()
    manifest['decodedSha256'] = hashlib.sha256(binary).hexdigest()
    manifest_path.write_text(json.dumps(manifest, separators=(',', ':')), 'utf-8')
    revision = {'video': manifest['source']['sha256'], 'trace': manifest['payloadSha256'], 'target': manifest['target']['sha256']}
    (ROOT / 'packages/webui/src/lib/login-media-version.json').write_text(json.dumps(revision, indent=2) + '\n', 'utf-8')
    report = {'frames': len(statistics), 'metricDefinition': 'Symmetric 75th-percentile shape residual after local optical flow; not semantic edge ground truth. Splits, merges, occlusions and ambiguous matches start new path identities.',
              'payloadSha256': manifest['payloadSha256'], 'maxResidualPx': max(record['maxResidualPx'] for record in statistics), 'samples': statistics}
    review = ROOT / '.tmp-dev/login-trace-review'
    review.mkdir(parents=True, exist_ok=True)
    (review / 'tracking-report.json').write_text(json.dumps(report, indent=2), 'utf-8')
    print(f'Retracked all {len(statistics)} frames; {len(compressed):,} compressed bytes', flush=True)
    write_preview(ROOT, review)


if __name__ == '__main__':
    main()
