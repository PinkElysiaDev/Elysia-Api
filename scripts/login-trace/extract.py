import argparse
import gzip
import hashlib
import json
import math
import struct
import sys
from pathlib import Path
from preview import write_preview

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / '.tmp-dev/vision-python'))

import cv2
import numpy as np

COLORS = [(239, 173, 255), (100, 238, 255), (222, 255, 100), (255, 191, 112)]


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def write_tracking_report(directory, manifest, statistics):
    samples = [{'frame': record['frame'], 'time': record['time'], 'paths': record['paths'], 'matched': record['matchedPaths'],
                'maxResidualPx': record['maxTrackingResidualPx']} for record in statistics]
    report = {'frames': len(samples), 'payloadSha256': manifest['payloadSha256'],
              'metricDefinition': 'Symmetric 75th-percentile shape residual after local optical flow; not semantic edge ground truth. Splits, merges, occlusions and ambiguous matches start new path identities.',
              'maxResidualPx': max(record['maxResidualPx'] for record in samples), 'samples': samples}
    (directory / 'tracking-report.json').write_text(json.dumps(report, indent=2), 'utf-8')


def polygon_mask(size, polygon):
    result = np.zeros(size, np.uint8)
    cv2.fillPoly(result, [np.array(polygon, np.int32)], 255)
    return result


def stroke_paths(skeleton, labels, minimum=12):
    height, width = skeleton.shape
    rows, columns = np.nonzero(skeleton)
    pixels = set((rows * width + columns).tolist())
    neighbors = {}
    for pixel in pixels:
        adjacent = []
        for offset in (-width - 1, -width, -width + 1, -1, 1, width - 1, width, width + 1):
            candidate = pixel + offset
            if candidate not in pixels or abs(candidate % width - pixel % width) > 1:
                continue
            if abs(offset) not in (1, width):
                if pixel + (1 if offset in (-width + 1, width + 1) else -1) in pixels:
                    continue
                if pixel + (width if offset > 0 else -width) in pixels:
                    continue
            adjacent.append(candidate)
        neighbors[pixel] = adjacent
    visited = set()
    paths = []
    starts = sorted(pixels, key=lambda pixel: (len(neighbors[pixel]) == 2, pixel))
    for start in starts:
        for following in neighbors[start]:
            edge = (min(start, following), max(start, following))
            if edge in visited:
                continue
            chain = [start]
            previous, current = start, following
            while True:
                visited.add((min(previous, current), max(previous, current)))
                chain.append(current)
                if current == start or len(neighbors[current]) != 2:
                    break
                candidate = next(pixel for pixel in neighbors[current] if pixel != previous)
                if (min(current, candidate), max(current, candidate)) in visited:
                    break
                previous, current = current, candidate
            points = np.array([(pixel % width, pixel // width) for pixel in chain], np.float32)
            length = float(cv2.arcLength(points, False))
            if length < minimum:
                continue
            closed = chain[0] == chain[-1]
            simplified = cv2.approxPolyDP(points, 0.65, closed).reshape(-1, 2)
            if len(simplified) < 2:
                continue
            middle = points[len(points) // 2].astype(int)
            group = int(labels[middle[1], middle[0]]) - 1
            if group != 1 and length < 24:
                continue
            paths.append({'group': max(0, group), 'closed': closed, 'points': simplified, 'length': length})
    return paths


def tracking_samples(path):
    points = path['points']
    if path['closed']:
        points = np.vstack([points, points[0]])
    distances = np.r_[0, np.cumsum(np.linalg.norm(np.diff(points, axis=0), axis=1))]
    sample_distances = np.linspace(0, distances[-1], 16, endpoint=not path['closed'])
    samples = np.column_stack([np.interp(sample_distances, distances, points[:, axis]) for axis in range(2)]).astype(np.float32)
    return samples, float(distances[-1])


def track_ids(paths, previous_paths, previous_gray, gray, next_id):
    candidates = {}
    if previous_paths:
        previous_samples = [tracking_samples(path) for path in previous_paths]
        anchors = np.array([samples[0][::3] for samples in previous_samples], np.float32)
        moved, status, errors = cv2.calcOpticalFlowPyrLK(previous_gray, gray, anchors.reshape(-1, 1, 2), None, winSize=(21, 21), maxLevel=3)
        moved = moved.reshape(anchors.shape)
        valid = ((status[:, 0] > 0) & (errors[:, 0] < 20)).reshape(anchors.shape[:2])
        for index, path in enumerate(previous_paths):
            if valid[index].sum() < 3:
                continue
            displacement = np.median((moved[index] - anchors[index])[valid[index]], axis=0)
            if np.linalg.norm(displacement) > 8:
                continue
            samples, length = previous_samples[index]
            predicted = samples + displacement
            center = predicted.mean(axis=0)
            cell = (path['group'], int(center[0] // 12), int(center[1] // 12))
            candidates.setdefault(cell, []).append((path, predicted, center, length))
    matches = []
    for index, path in enumerate(paths):
        samples, length = tracking_samples(path)
        center = samples.mean(axis=0)
        cell_x, cell_y = int(center[0] // 12), int(center[1] // 12)
        for offset_x in (-1, 0, 1):
            for offset_y in (-1, 0, 1):
                for previous, predicted, predicted_center, previous_length in candidates.get((path['group'], cell_x + offset_x, cell_y + offset_y), []):
                    ratio = length / max(previous_length, 0.001)
                    distance = float(np.linalg.norm(center - predicted_center))
                    if previous['closed'] != path['closed'] or not 0.75 < ratio < 1.35 or distance > 7:
                        continue
                    pairwise = np.linalg.norm(samples[:, None] - predicted[None, :], axis=2)
                    nearest = np.r_[pairwise.min(axis=0), pairwise.min(axis=1)]
                    error = float(np.percentile(nearest, 75))
                    if error <= 2.5 and nearest.max() <= 5:
                        matches.append((error + distance * 0.1 + abs(math.log(ratio)), index, previous['id'], error))
    used, assigned = set(), set()
    for _, index, identifier, error in sorted(matches):
        if identifier in used or index in assigned:
            continue
        paths[index]['id'] = identifier
        paths[index]['trackingErrorPx'] = error
        used.add(identifier)
        assigned.add(index)
    for index, path in enumerate(paths):
        if index not in assigned:
            path['id'] = next_id
            next_id += 1
    return next_id


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--limit', type=int, default=0)
    parser.add_argument('--refresh-metadata', action='store_true')
    parser.add_argument('--review-dir', type=Path, default=ROOT / '.tmp-dev/login-trace-review')
    args = parser.parse_args()
    if args.refresh_metadata:
        manifest_path = ROOT / 'packages/webui/public/assets/elysia-character-trace.json'
        manifest = json.loads(manifest_path.read_text('utf-8'))
        payload = gzip.decompress((manifest_path.parent / manifest['payload']).read_bytes())
        manifest['decodedSha256'] = hashlib.sha256(payload).hexdigest()
        manifest_path.write_text(json.dumps(manifest, separators=(',', ':')), 'utf-8')
        report_path = args.review_dir / 'report.json'
        if report_path.exists():
            statistics = json.loads(report_path.read_text('utf-8'))['frames']
            if all('matchedPaths' in record for record in statistics):
                write_tracking_report(args.review_dir, manifest, statistics)
        return
    cv2.setNumThreads(4)
    annotations_path = Path(__file__).with_name('annotations.json')
    annotations = json.loads(annotations_path.read_text('utf-8'))
    assets = ROOT / 'packages/webui/public/assets'
    source_path = assets / 'elysia-login.mp4'
    target_path = assets.parent / 'role-mask.png'
    capture = cv2.VideoCapture(str(source_path))
    frame_rate = capture.get(cv2.CAP_PROP_FPS)
    total_frames = int(capture.get(cv2.CAP_PROP_FRAME_COUNT))
    success, reference = capture.read()
    if not success:
        raise RuntimeError('Cannot decode the source video')
    height, width = reference.shape[:2]
    if [width, height] != annotations['sourceSize']:
        raise RuntimeError('Annotation resolution differs from the video')
    reference_gray = cv2.cvtColor(reference, cv2.COLOR_BGR2GRAY)
    regions = []
    for region in annotations['sourceRegions']:
        mask = polygon_mask((height, width), region['polygon'])
        features = cv2.goodFeaturesToTrack(reference_gray, 90, 0.025, 8, mask=mask)
        regions.append({'trackWith': region.get('trackWith'), 'group': region['group'], 'mask': mask, 'features': features, 'guess': features.copy(), 'transform': np.eye(2, 3, dtype=np.float32)})
    exclusions = [polygon_mask((height, width), polygon) for polygon in annotations['sourceExclusions']]
    args.review_dir.mkdir(parents=True, exist_ok=True)
    movie = cv2.VideoWriter(str(args.review_dir / 'overlay.mp4'), cv2.VideoWriter_fourcc(*'mp4v'), frame_rate, (width, height))
    binary = bytearray()
    offsets, timestamps, statistics, reviews = [], [], [], []
    previous_paths, previous_gray, next_id = [], reference_gray, 1
    sheet = np.zeros((4 * 208, 4 * 360, 3), np.uint8)
    sheet_index = 0
    capture.set(cv2.CAP_PROP_POS_FRAMES, 0)
    review_frames = {round(stamp * frame_rate) for stamp in annotations['reviewTimes']}
    limit = min(total_frames, args.limit) if args.limit else total_frames
    for frame_index in range(limit):
        success, frame = capture.read()
        if not success:
            raise RuntimeError(f'Unexpected decode failure at frame {frame_index}')
        stamp = capture.get(cv2.CAP_PROP_POS_MSEC) / 1000
        if timestamps and stamp <= timestamps[-1]:
            raise RuntimeError('Decoder did not return increasing presentation timestamps')
        timestamps.append(round(stamp, 9))
        gray = cv2.cvtColor(frame, cv2.COLOR_BGR2GRAY)
        labels = np.zeros((height, width), np.uint8)
        tracking_counts = []
        for region in regions:
            moved, status, errors = cv2.calcOpticalFlowPyrLK(reference_gray, gray, region['features'], region['guess'].copy(), winSize=(41, 41), maxLevel=4, flags=cv2.OPTFLOW_USE_INITIAL_FLOW)
            valid = (status[:, 0] > 0) & (errors[:, 0] < 28)
            tracking_counts.append(int(valid.sum()))
            if valid.sum() >= 6:
                transform, _ = cv2.estimateAffinePartial2D(region['features'][valid], moved[valid], method=cv2.RANSAC, ransacReprojThreshold=5)
                if transform is not None and 0.75 < np.linalg.det(transform[:, :2]) < 1.35:
                    region['transform'] = transform
            region['guess'] = moved
            if region['trackWith'] is not None:
                region['transform'] = regions[region['trackWith']]['transform']
            warped = cv2.warpAffine(region['mask'], region['transform'], (width, height), flags=cv2.INTER_NEAREST)
            dilation = 17 if region['group'] == 2 else 9
            warped = cv2.dilate(warped, np.ones((dilation, dilation), np.uint8))
            labels[warped > 0] = region['group'] + 1
        for exclusion in exclusions:
            warped = cv2.warpAffine(exclusion, regions[1]['transform'], (width, height), flags=cv2.INTER_NEAREST)
            labels[warped > 0] = 0
        for correction in annotations['corrections']:
            if correction['startFrame'] <= frame_index <= correction['endFrame']:
                region_mask = polygon_mask((height, width), correction['polygon'])
                labels[region_mask > 0] = 0 if correction['exclude'] else correction['group'] + 1
        softened = cv2.GaussianBlur(gray, (3, 3), 0.55)
        ink = cv2.morphologyEx(softened, cv2.MORPH_BLACKHAT, cv2.getStructuringElement(cv2.MORPH_ELLIPSE, (9, 9)))
        candidates = np.where((ink >= 7) & (labels > 0), 255, 0).astype(np.uint8)
        candidates[:3] = 0
        candidates[-3:] = 0
        foreground = np.where(labels > 0, 255, 0).astype(np.uint8)
        boundary_distance = cv2.distanceTransform(foreground, cv2.DIST_L2, 3)
        edges = cv2.Canny(softened, 28, 75)
        nearby_ink = cv2.dilate(candidates, cv2.getStructuringElement(cv2.MORPH_ELLIPSE, (7, 7)))
        candidates[(edges > 0) & (boundary_distance > 0) & (boundary_distance < 15) & (nearby_ink == 0)] = 255
        skeleton = cv2.ximgproc.thinning(candidates)
        paths = stroke_paths(skeleton, labels)
        next_id = track_ids(paths, previous_paths, previous_gray, gray, next_id)
        offsets.append(len(binary))
        binary.extend(struct.pack('<I', len(paths)))
        overlay = frame.copy()
        lines = np.zeros_like(frame)
        raster = np.zeros_like(gray)
        for path in paths:
            points = path['points']
            encoded = np.rint(points).astype('<i2')
            encoded[1:] = np.diff(encoded, axis=0)
            binary.extend(struct.pack('<IBBH', path['id'], path['group'], int(path['closed']), len(points)))
            binary.extend(encoded.tobytes())
            integers = np.rint(points).astype(np.int32)
            cv2.polylines(overlay, [integers], path['closed'], COLORS[path['group']], 1, cv2.LINE_AA)
            cv2.polylines(lines, [integers], path['closed'], COLORS[path['group']], 1, cv2.LINE_AA)
            cv2.polylines(raster, [integers], path['closed'], 255, 1)
        distance = cv2.distanceTransform(255 - skeleton, cv2.DIST_L2, cv2.DIST_MASK_PRECISE)
        error_values = distance[raster > 0]
        maximum = float(error_values.max()) if len(error_values) else 0
        record = {'frame': frame_index, 'time': stamp, 'paths': len(paths), 'vertices': sum(len(path['points']) for path in paths), 'groups': [sum(path['group'] == group for path in paths) for group in range(4)], 'maxCenterlineDeviationPx': round(maximum, 3), 'trackedFeatures': tracking_counts}
        tracked_errors = [path['trackingErrorPx'] for path in paths if 'trackingErrorPx' in path]
        record['matchedPaths'] = len(tracked_errors)
        record['maxTrackingResidualPx'] = max(tracked_errors, default=0)
        statistics.append(record)
        movie.write(overlay)
        tile_index = frame_index % 16
        tile_y, tile_x = (tile_index // 4) * 208, (tile_index % 4) * 360
        sheet[tile_y:tile_y + 202, tile_x:tile_x + 360] = cv2.resize(overlay, (360, 202))
        cv2.putText(sheet, f'{frame_index} / {stamp:.3f}s', (tile_x + 5, tile_y + 17), cv2.FONT_HERSHEY_SIMPLEX, 0.4, (255, 255, 255), 1)
        if tile_index == 15 or frame_index == limit - 1:
            cv2.imwrite(str(args.review_dir / f'contact-{sheet_index:03}.jpg'), sheet)
            sheet_index += 1
        if frame_index in review_frames:
            cv2.imwrite(str(args.review_dir / f'frame-{frame_index:04}.png'), frame)
            cv2.imwrite(str(args.review_dir / f'overlay-{frame_index:04}.png'), overlay)
            cv2.imwrite(str(args.review_dir / f'lines-{frame_index:04}.png'), lines)
            reviews.append({'frame': frame_index, 'time': stamp})
        previous_paths, previous_gray = paths, gray
        if frame_index % 60 == 0:
            print(f'{frame_index}/{limit}: {len(paths)} paths, max centerline deviation {maximum:.2f}px', flush=True)
    movie.release()
    capture.release()
    target = cv2.imread(str(target_path), cv2.IMREAD_UNCHANGED)
    target_height, target_width = target.shape[:2]
    target_labels = np.zeros((target_height, target_width), np.uint8)
    for region in annotations['targetRegions']:
        target_labels[polygon_mask(target_labels.shape, region['polygon']) > 0] = region['group'] + 1
    target_skeleton = cv2.ximgproc.thinning(np.where(target[:, :, 3] > 100, 255, 0).astype(np.uint8))
    target_paths = stroke_paths(target_skeleton, target_labels, minimum=6)
    target_data = [{'id': index, 'group': path['group'], 'closed': path['closed'], 'points': np.round(path['points'] / [target_width, target_height], 6).reshape(-1).tolist()} for index, path in enumerate(target_paths)]
    report = {'frameCount': limit, 'expectedFrames': total_frames, 'allGroupsPresent': all(all(record['groups']) for record in statistics), 'maxCenterlineDeviationPx': max(record['maxCenterlineDeviationPx'] for record in statistics), 'metricDefinition': 'Distance from exported rasterized polylines to the current-frame extracted stroke skeleton; this is NOT independent semantic ground truth.', 'visualReview': 'Keyframe images, consecutive-frame contact sheets and full-resolution replay are supplied for manual review; generation alone does not certify semantic accuracy.', 'frames': statistics, 'reviewFrames': reviews}
    (args.review_dir / 'report.json').write_text(json.dumps(report, indent=2), 'utf-8')
    if args.limit:
        print(f'Preview only: {args.review_dir}', flush=True)
        return
    compressed = gzip.compress(bytes(binary), compresslevel=9, mtime=0)
    payload_name = 'elysia-character-trace.bin.gz'
    (assets / payload_name).write_bytes(compressed)
    manifest = {'version': 1, 'coordinateEncoding': 'pixel-delta-i16', 'source': {'file': source_path.name, 'sha256': digest(source_path), 'width': width, 'height': height, 'duration': total_frames / frame_rate, 'frameRate': frame_rate}, 'target': {'file': target_path.name, 'sha256': digest(target_path), 'width': target_width, 'height': target_height, 'paths': target_data}, 'annotationSha256': digest(annotations_path), 'groups': annotations['groups'], 'payload': payload_name, 'payloadSha256': hashlib.sha256(compressed).hexdigest(), 'decodedBytes': len(binary), 'frameTimes': timestamps, 'frameOffsets': offsets, 'metrics': {key: value for key, value in report.items() if key not in ('frames', 'reviewFrames')}}
    manifest['decodedSha256'] = hashlib.sha256(binary).hexdigest()
    (assets / 'elysia-character-trace.json').write_text(json.dumps(manifest, separators=(',', ':')), 'utf-8')
    write_tracking_report(args.review_dir, manifest, statistics)
    revision = {'video': manifest['source']['sha256'], 'trace': manifest['payloadSha256'], 'target': manifest['target']['sha256']}
    (ROOT / 'packages/webui/src/lib/login-media-version.json').write_text(json.dumps(revision, indent=2) + '\n', 'utf-8')
    print(f'Exported {limit} frames, {len(compressed):,} compressed bytes. Review: {args.review_dir}', flush=True)
    write_preview(ROOT, args.review_dir)


if __name__ == '__main__':
    main()
