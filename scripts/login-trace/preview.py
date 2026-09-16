import argparse
import base64
import html
import json
import os
from pathlib import Path


def write_preview(root, destination):
    assets = root / 'packages/webui/public/assets'
    manifest = json.loads((assets / 'elysia-character-trace.json').read_text('utf-8'))
    template = Path(__file__).with_name('review.html').read_text('utf-8')
    video = Path(os.path.relpath(assets / manifest['source']['file'], destination)).as_posix()
    document = template.replace('__VIDEO__', html.escape(video, quote=True))
    document = document.replace('__MANIFEST__', json.dumps(manifest, separators=(',', ':')))
    document = document.replace('__PAYLOAD__', base64.b64encode((assets / manifest['payload']).read_bytes()).decode('ascii'))
    destination.mkdir(parents=True, exist_ok=True)
    (destination / 'index.html').write_text(document, 'utf-8')
    print(f'Frame-by-frame viewer: {destination / "index.html"}', flush=True)


if __name__ == '__main__':
    root = Path(__file__).resolve().parents[2]
    parser = argparse.ArgumentParser()
    parser.add_argument('--review-dir', type=Path, default=root / '.tmp-dev/login-trace-review')
    arguments = parser.parse_args()
    write_preview(root, arguments.review_dir)
