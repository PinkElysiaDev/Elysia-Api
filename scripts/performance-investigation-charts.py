"""Render one performance capture; never run workloads or change source code.

Requires matplotlib/numpy. Run only after measurement/profile collection stops.
By default existing pprof top files are read. --refresh-profile-tops explicitly
invokes local go tool pprof for complete flat tables, without starting a server.
"""

from __future__ import annotations

import argparse
from collections import Counter, defaultdict
from dataclasses import dataclass
from datetime import datetime, timezone
import html
import json
import math
import os
from pathlib import Path
import re
import statistics
import subprocess
from urllib.parse import quote

import matplotlib

matplotlib.use("Agg")
import matplotlib.pyplot as plt
import numpy as np


MIB = 1 << 20
DEFAULT_ROOT = ".tmp-dev/performance-investigation-2026-10-23"
METRICS = (
    ("cpuMS", "CPU ms / request", 1),
    ("latencyMS", "Full consumption ms", 1),
    ("bytes", "Allocated MiB / request", MIB),
    ("heapMiB", "Sampled heap peak MiB", 1),
    ("rssMiB", "Working-set peak MiB", 1),
    ("allocations", "Allocated objects / request", 1),
)
PROFILE_SAMPLES = ("cpu", "alloc_space", "alloc_objects", "inuse_space", "inuse_objects")
HEAP_PROFILES = ("heap-active.pprof", "heap-drained.pprof", "heap-held.pprof", "heap-cancelled.pprof")
RUN_SUFFIX = re.compile(r"-\d+-(current|release|merged|normalized)$")
NONFORMAL_RUN = re.compile(r"^(smoke|custom-smoke|sustain|timeline|trace)-")
TOP_ROW = re.compile(r"^\s*(\S+)\s+\S+%\s+\S+%\s+\S+\s+\S+%\s+(.+?)\s*$")
QUANTITY = re.compile(r"^([+-]?(?:\d+(?:\.\d*)?|\.\d+)(?:[eE][+-]?\d+)?)([A-Za-zµ]*)$")
UNITS = {
    "": 1, "ns": 1e-9, "us": 1e-6, "µs": 1e-6, "ms": 1e-3, "s": 1,
    "B": 1, "kB": 1024, "KB": 1024, "MB": MIB, "GB": 1 << 30,
    "TB": 1 << 40, "k": 1e3, "K": 1e3, "M": 1e6, "G": 1e9,
}
# First match wins: every flat sample belongs to exactly one category.
FLAT_CATEGORIES = (
    ("Profiler / diagnostics", r"^runtime/pprof|^internal/profile"),
    ("Windows observer", r"httpDisconnect|probeWindowsTCP|isClosedWindowsSocket"),
    ("Logging / redaction", r"sanitizeBody|redact|usageAsset|assetSink|finalizeDownstreamBody"),
    ("GC / allocation runtime", r"^runtime\.(?:gc|malloc|newobject|makeslice|growslice|scan|sweep|mark|typedmem|memclr|typePointers|heap|span|findObject|tryDefer)|^internal/runtime/gc"),
    ("Memory copy", r"memmove|memcpy|stringslite\.Clone|bytealg\.MakeNoZero|dirtmake\.Bytes"),
    ("String scan / regexp", r"regexp\.|jsonStringEnd|jsonValueEnd|needsSemanticCompaction|utf8\.|strings\.(?:Count|Index|Contains)|stringslite\.(?:Index|Count)|index(?:byte)?body|countbody|bytealg\.(?:Index|Count)"),
    ("JSON codec / reflection", r"sonic|encoding/json|jsontext|^decode_|^encode_|reflect\.|encodeValueContainer|compactValueString|\.Value\.(?:Decode|read|Bytes)|\.StringValue|\.ParseValue|builtin\.(?:object|array|readArray|stringValue)"),
    ("Usage / metadata logic", r"MergeUsage|mergeCounter|updateRecordProtocolUsage|[Mm]etadata|[Ll]ogprob|decodeUsage"),
    ("Protocol / conversion", r"github\.com/elysia-api/backend/protocol"),
    ("Storage / persistence", r"sqlite|database/sql|backend/storage|persistUsage|saveUsage"),
    ("HTTP / framing / syscall", r"hertz|go-sse|eventstream|net\.|syscall\.|internal/poll|bufiox|bufio|responseStream"),
    ("Scheduling / other runtime", r"^runtime\.|^internal/runtime|^sync\.|^time\.|^context\."),
    ("Other", r".*"),
)


def read_json(path: Path, default=None):
    if not path.exists():
        return default
    return json.loads(path.read_text(encoding="utf-8-sig"))


def number(value):
    return value if isinstance(value, (int, float)) and math.isfinite(value) else None


def distribution(values):
    values = [value for value in values if number(value) is not None]
    if not values:
        return {"median": None, "minimum": None, "maximum": None}
    return {"median": statistics.median(values), "minimum": min(values), "maximum": max(values)}


def scenario_name(run):
    name = run.get("name", "")
    suffix = f"-{run.get('pair')}-{run.get('version', 'current')}"
    return name[:-len(suffix)] if name.endswith(suffix) else RUN_SUFFIX.sub("", name)


def is_formal_scenario(name):
    return NONFORMAL_RUN.match(name) is None


def link(path: Path, root: Path):
    return quote(Path(os.path.relpath(path, root)).as_posix(), safe="/:")


def safe_name(text):
    return re.sub(r"[^A-Za-z0-9_.-]+", "-", text)


def format_value(value):
    if number(value) is None:
        return "—"
    if abs(value) >= 1000:
        return f"{value:,.0f}"
    if value and abs(value) < 0.01:
        return f"{value:.3g}"
    return f"{value:.2f}"


def fixture_details(run, row=None):
    row = row or {}
    fixture, env = run.get("fixture", {}), run.get("environment", {})
    return {
        **fixture,
        "stream": bool(row.get("Stream", fixture.get("stream", env.get("ELYSIA_FLAME_STREAM") == "sse"))),
        "mapping": fixture.get("mapping") or env.get("ELYSIA_REVIEW_MAPPING") or "none",
        "logprobs": bool(fixture.get("logprobs") or env.get("ELYSIA_REVIEW_LOGPROBS")),
        "preparation": run.get("preparation", "default"),
        "memoryLimit": env.get("GOMEMLIMIT", "unknown"),
    }


def metric_sample(row):
    metrics, count = row.get("Metrics", {}), row.get("Requests", 0)
    if not count:
        return None
    def scaled(key, factor=1):
        value = number(metrics.get(key))
        return value / factor if value is not None else None
    latency = row.get("LatencyP50P95P99NS", [])
    return {
        "requests": count,
        "bytes": scaled("Allocated", count),
        "allocations": scaled("Allocations", count),
        "cpuMS": scaled("CPUTimeNS", count * 1e6),
        "latencyMS": latency[0] / 1e6 if latency and number(latency[0]) is not None else None,
        "heapMiB": scaled("PeakHeap", MIB),
        "rssMiB": scaled("PeakWorkingSet", MIB),
        "upstreamCalls": row.get("UpstreamCalls"),
        "droppedSamples": metrics.get("DroppedSamples"),
        "preparation": metrics.get("Preparation"),
    }


def load_runs(capture: Path, warnings):
    runs = []
    for directory in sorted((capture / "runs").glob("*")):
        if not directory.is_dir():
            continue
        result = read_json(directory / "result.json")
        if result is None:
            warnings.append(f"Incomplete run directory: {directory.name} has no result.json")
            runs.append({"name": directory.name, "status": "incomplete", "directory": directory, "rows": []})
            continue
        result = {**result, "directory": directory, "rows": read_json(directory / "measurements.json", [])}
        supplemental = [directory / name for name in ("tool-roundtrip.json", "held-progress.json", "held.json") if (directory / name).is_file()]
        result["supplemental"] = supplemental
        if result.get("status") == "passed" and not result["rows"] and not supplemental:
            warnings.append(f"No measurements.json rows: {directory.name} (may be profile-only)")
        runs.append(result)
    return runs


def scenario_summaries(capture, runs, warnings):
    supplied = read_json(capture / "summary.json", {})
    metadata, samples = {}, defaultdict(list)
    for run in runs:
        name, version = scenario_name(run), run.get("version", "unknown")
        for row in run["rows"]:
            if row.get("Mode") != "measure" or row.get("Phase") == "first-request":
                continue
            if not is_formal_scenario(name) or run.get("status") != "passed":
                continue
            sample = metric_sample(row)
            if sample is not None:
                key = (name, version)
                samples[key].append(sample)
                metadata[key] = {"fixture": fixture_details(run, row), "revision": run.get("revision", "unknown")}
    groups = []
    for (name, version), rows in sorted(samples.items()):
        groups.append({
            "name": name, "version": version, "repeats": len(rows),
            "requests": sum(row["requests"] for row in rows),
            **{metric: distribution(row[metric] for row in rows) for metric, _, _ in METRICS},
            "preparationObserved": {
                "activePeak": distribution((row.get("preparation") or {}).get("PeakActive") for row in rows),
                "queuedPeak": distribution((row.get("preparation") or {}).get("PeakQueued") for row in rows),
            },
            **metadata[(name, version)], "samples": rows,
        })
    # Raw completed runs are authoritative when a summary predates collection.
    recorded = {(group["name"], group["version"]): group for group in groups}
    for summary in supplied.get("summaries", []):
        if not is_formal_scenario(summary["name"]):
            continue
        for version, values in summary["versions"].items():
            current = recorded.get((summary["name"], version))
            if current is None or any(current[key] != values.get(key) for key in ("repeats", "requests")):
                warnings.append(f"summary.json differs from completed raw runs: {summary['name']} {version}; charts use raw measurements")
    if not supplied:
        warnings.append("capture/summary.json absent: formal Mode=measure rows were summarized directly; no comparison percentages inferred")
    return groups, supplied.get("definitions", {})


@dataclass
class Figures:
    root: Path
    title: str
    items: list

    def save(self, figure, name, caption, kind):
        directory = self.root / "charts"
        figure.savefig(directory / f"{name}.png", bbox_inches="tight")
        figure.savefig(directory / f"{name}.svg", bbox_inches="tight")
        plt.close(figure)
        self.items.append({"name": name, "title": caption, "kind": kind, "png": f"charts/{name}.png", "svg": f"charts/{name}.svg"})


def plot_cost_matrix(groups, figures):
    if not groups:
        return
    values = np.array([[group.get(key, {}).get("median") / divisor if number(group.get(key, {}).get("median")) is not None else np.nan for key, _, divisor in METRICS] for group in groups])
    colors = np.full(values.shape, np.nan)
    for column in range(values.shape[1]):
        finite = np.isfinite(values[:, column])
        if not finite.any():
            continue
        transformed = np.log1p(np.maximum(values[finite, column], 0))
        span = transformed.max() - transformed.min()
        colors[finite, column] = (transformed - transformed.min()) / span if span else 0.5
    figure, axis = plt.subplots(figsize=(15, max(5, len(groups) * 0.34 + 2)))
    palette = plt.get_cmap("YlOrRd").copy()
    palette.set_bad("#e5e7eb")
    chart = axis.imshow(colors, cmap=palette, vmin=0, vmax=1, aspect="auto")
    for row, group in enumerate(groups):
        for column in range(len(METRICS)):
            axis.text(column, row, format_value(values[row, column]), ha="center", va="center", fontsize=8.5, color="white" if colors[row, column] > 0.7 else "#132238")
    axis.set_xticks(range(len(METRICS)), [item[1] for item in METRICS], rotation=15, ha="right")
    axis.set_yticks(range(len(groups)), [f"{group['name'].removeprefix('measure-')} [{group['version']}; n={group['repeats']}]" for group in groups])
    axis.set_title(figures.title + "\nRecorded medians; each column independently scaled with log1p\nColors do NOT compare absolute cost across columns; full ranges are in the table")
    figure.colorbar(chart, ax=axis, label="Within-column relative color (0–1); missing values are gray")
    figure.tight_layout()
    figures.save(figure, "scenario-costs", "Scenario × resource cost; independent column colors, actual median units", "measurements")


def plot_series(figure, axes, selections, x_key, x_label, title, metrics):
    axes = list(axes)
    for label, rows in sorted(selections.items()):
        rows = sorted(rows, key=lambda row: row["fixture"].get(x_key, 0))
        if x_key == "size":
            peaks = [row.get("preparationObserved", {}).get("activePeak", {}).get(bound) for row in rows for bound in ("minimum", "maximum")]
            peaks = [peak for peak in peaks if number(peak) is not None]
            if peaks:
                label += f"; observed active peaks {format_value(min(peaks))}–{format_value(max(peaks))}"
        x = [row["fixture"].get(x_key) for row in rows]
        if any(value is None for value in x):
            continue
        for axis, (metric, unit, divisor) in zip(axes, metrics):
            values = [row.get(metric, {}).get("median") for row in rows]
            if any(number(value) is None for value in values):
                continue
            y = np.array(values) / divisor
            low = [(row[metric]["median"] - row[metric]["minimum"]) / divisor for row in rows]
            high = [(row[metric]["maximum"] - row[metric]["median"]) / divisor for row in rows]
            axis.errorbar(x, y, yerr=[low, high], marker="o", capsize=3, label=label)
            axis.set_ylabel(unit)
            axis.set_xlabel(x_label)
            axis.grid(alpha=0.2)
    for axis in axes:
        if axis.has_data():
            axis.legend(fontsize=8)
    figure.suptitle(title + "\nMedian and full recorded range; distinct fixture settings remain separate")
    figure.tight_layout(rect=(0, 0, 1, 0.9))


def plot_scale_trends(groups, figures):
    images, probabilities = defaultdict(list), defaultdict(list)
    for group in groups:
        fixture = group.get("fixture", {})
        protocol = f"{fixture.get('source', '?')}→{fixture.get('target', '?')}"
        mode = "SSE" if fixture.get("stream") else "JSON"
        identity = f"{group['version']} {protocol} {mode} C{fixture.get('workers', '?')} {fixture.get('memoryLimit', '?')} prep={fixture.get('preparation', '?')}"
        if group["name"].startswith("after-study-"):
            identity += "; after-study control repeat"
        if fixture.get("workload") == "image" and fixture.get("log", "off") == "off" and fixture.get("mapping", "none") == "none" and fixture.get("candidates", 1) == 1:
            images[identity].append(group)
        if fixture.get("stream") and fixture.get("workload") == "small" and fixture.get("request") == "short" and ("logprobs" in group["name"] or "stream" in group["name"]):
            identity += f" {'probability' if fixture.get('logprobs') else 'plain'}; frame={fixture.get('frameBytes', '?')}B"
            probabilities[identity].append(group)
    for name, series, dimension, label, metrics in (
        ("image-size", images, "size", "Input base64 image text (MiB)", [METRICS[0], METRICS[2], METRICS[3], METRICS[4]]),
        ("logprobs-scale", probabilities, "frames", "Provider frames", [METRICS[0], METRICS[1], METRICS[2], METRICS[5]]),
    ):
        if not series:
            continue
        figure, grid = plt.subplots(2, 2, figsize=(16, 11))
        plot_series(figure, grid.flat, series, dimension, label, figures.title + " | " + name, metrics)
        figures.save(figure, name, name + ": scale trends, settings separated", "trends")


def plot_variants(groups, figures):
    selected = [group for group in groups if group.get("fixture", {}).get("workload") == "image" or group.get("fixture", {}).get("mapping", "none") != "none"]
    if not selected:
        return
    figure, axes = plt.subplots(2, 2, figsize=(17, max(9, len(selected) * 0.38)))
    for axis, (metric, unit, divisor) in zip(axes.flat, [METRICS[0], METRICS[1], METRICS[2], METRICS[4]]):
        values = [group.get(metric, {}).get("median") for group in selected]
        y = [value / divisor if number(value) is not None else np.nan for value in values]
        low = [(group[metric]["median"] - group[metric]["minimum"]) / divisor if number(group.get(metric, {}).get("median")) is not None else 0 for group in selected]
        high = [(group[metric]["maximum"] - group[metric]["median"]) / divisor if number(group.get(metric, {}).get("median")) is not None else 0 for group in selected]
        axis.barh(range(len(selected)), y, color="#147d92", xerr=[low, high], capsize=2)
        axis.set_yticks(range(len(selected)), [group["name"].removeprefix("measure-") for group in selected], fontsize=8)
        axis.invert_yaxis()
        axis.set_xlabel(unit)
        axis.grid(axis="x", alpha=0.2)
    figure.suptitle(figures.title + " | logging / mapping / payload variants\nDifferent names can change several factors; compare matching fixture settings only")
    figure.tight_layout(rect=(0, 0, 1, 0.94))
    figures.save(figure, "logging-mapping-variants", "Logging and mapping variants: medians with full ranges", "variants")


def plot_timelines(runs, figures, warnings):
    grouped = defaultdict(list)
    for run in runs:
        for row in run["rows"]:
            if run.get("status") == "passed" and row.get("Phase") != "first-request" and row.get("Mode") == "measure" and row.get("Metrics", {}).get("Timeline"):
                grouped[scenario_name(run)].append((run, row))
    patterns = (r"^unrestricted-image31.*four", r"^measure-image31.*four", r"^measure-image31(?!.*four)", r"^after-study-image31$", r"^after-study-image31-after$", r"logprobs.*1024", r"stream.*256", r"nodes", r"chat-short")
    selected = []
    for pattern in patterns:
        candidates = [name for name in sorted(grouped) if re.search(pattern, name) and name not in selected]
        if candidates:
            selected.append(candidates[0])
    for name in selected:
        candidates = grouped[name]
        run, row = min(candidates, key=lambda pair: (abs(pair[0].get("pair", 0) - 2), pair[0]["name"]))
        samples = row["Metrics"]["Timeline"]
        elapsed = np.array([sample["ElapsedMS"] for sample in samples]) / 1000
        figure, axes = plt.subplots(4, 1, figsize=(15, 12), sharex=True)
        for field, label in (("Heap", "HeapAlloc"), ("HeapInuse", "HeapInuse"), ("RSS", "Working set")):
            if all(field in sample for sample in samples):
                axes[0].plot(elapsed, [sample[field] / MIB for sample in samples], label=label)
        axes[0].set_ylabel("MiB")
        for field in ("ActiveRequests", "PreparationActive", "PreparationQueued", "QueueDepth"):
            if all(field in sample for sample in samples):
                axes[1].step(elapsed, [sample[field] for sample in samples], where="post", label=field)
        axes[1].set_ylabel("Requests / queue items")
        if all("GCCycles" in sample for sample in samples):
            axes[2].step(elapsed, [sample["GCCycles"] - samples[0]["GCCycles"] for sample in samples], where="post", label="GC cycles since first sample")
        axes[2].set_ylabel("Cycles")
        if all("GCPauseNS" in sample for sample in samples):
            axes[3].plot(elapsed, [(sample["GCPauseNS"] - samples[0]["GCPauseNS"]) / 1e6 for sample in samples], label="Cumulative stop-the-world pause")
        axes[3].set_ylabel("GC pause ms")
        axes[3].set_xlabel("Seconds since measurement reset")
        for axis in axes:
            axis.grid(alpha=0.2)
            if axis.has_data():
                axis.legend(loc="upper right")
            else:
                axis.text(0.03, 0.5, "This field was not recorded", transform=axis.transAxes)
        figure.suptitle(f"{figures.title}\n{run['name']} | selected repeat nearest index 2; {len(samples)} samples\n20 ms sampling, not exact peaks; GC baseline is the first sample")
        figure.tight_layout(rect=(0, 0, 1, 0.92))
        figures.save(figure, "timeline-" + safe_name(name), f"{run['name']} memory, GC and request timeline", "timeline")
    if not selected:
        warnings.append("No representative timeline samples available")


def parse_quantity(text):
    match = QUANTITY.fullmatch(text)
    if match is None or match[2] not in UNITS:
        raise ValueError(f"Unsupported pprof quantity: {text}")
    return float(match[1]) * UNITS[match[2]]


def parse_flat_top(path, sample):
    text = path.read_text(encoding="utf-8-sig")
    total_match = re.search(r"Total samples = (\S+)", text) if sample == "cpu" else re.search(r"\bof (\S+) total", text)
    if total_match is None:
        raise ValueError("pprof total denominator was not found")
    total = parse_quantity(total_match[1])
    functions = {}
    for line in text.splitlines():
        match = TOP_ROW.match(line)
        if match:
            # Only the first numeric column is used. The cum columns are ignored.
            function = match[2].removesuffix(" (inline)")
            functions[function] = functions.get(function, 0) + parse_quantity(match[1])
    categories = {name: 0.0 for name, _ in FLAT_CATEGORIES}
    for function, weight in functions.items():
        category = next(name for name, pattern in FLAT_CATEGORIES if re.search(pattern, function))
        categories[category] += weight
    listed = sum(functions.values())
    categories["Unlisted / rounding"] = max(0, total - listed)
    return {"total": total, "listedFlat": listed, "categories": categories, "functions": functions, "hasNegativeSamples": any(value < 0 for value in functions.values()), "header": text.split("flat", 1)[0].strip()}


def profile_candidates(capture, runs):
    metadata = {}
    for path in sorted(capture.glob("flamegraphs*.json")):
        rows = read_json(path, [])
        if not isinstance(rows, list):
            continue
        for item in rows:
            if not item.get("focus"):
                metadata[(item.get("run"), item.get("sample"), Path(item.get("profile", "")).name)] = item
    profiles = []
    for run in runs:
        directory = run["directory"]
        paths = []
        if (directory / "cpu.pprof").exists():
            paths.append(("cpu", directory / "cpu.pprof", None))
        allocation = directory / "alloc-difference.pprof"
        base = None
        if not allocation.exists() and (directory / "alloc-after.pprof").exists():
            allocation, base = directory / "alloc-after.pprof", directory / "alloc-before.pprof"
        if allocation.exists() and (base is None or base.exists()):
            paths.extend((sample, allocation, base) for sample in ("alloc_space", "alloc_objects"))
        for filename in HEAP_PROFILES:
            path = directory / filename
            if path.is_file():
                paths.extend((sample, path, None) for sample in ("inuse_space", "inuse_objects"))
        for sample, path, base in paths:
            detail = metadata.get((run["name"], sample, path.name), {})
            choices = [capture / detail["top"]] if detail.get("top") else []
            choices.append(directory / f"{path.name}-{sample}-top.txt")
            if not sample.startswith("inuse_"):
                choices.append(directory / f"{sample}-top.txt")
            if sample == "cpu":
                choices.append(directory / "cpu-top.txt")
            binary = Path(run.get("binary", run.get("command", "")))
            profiles.append({"run": run, "sample": sample, "path": path, "base": base, "top": next((file for file in choices if file.is_file()), None), "binary": binary, "metadata": detail})
    return profiles


def profile_hotspots(capture, runs, figures, refresh, warnings):
    profiles = []
    for candidate in profile_candidates(capture, runs):
        run, sample = candidate["run"], candidate["sample"]
        top = candidate["top"]
        if refresh:
            directory = figures.root / "charts" / "profile-tops"
            directory.mkdir(exist_ok=True)
            top = directory / f"{safe_name(run['name'])}-{candidate['path'].stem}-{sample}.txt"
            command = ["go", "tool", "pprof", "-top", "-nodecount=0", "-nodefraction=0", "-edgefraction=0", f"-sample_index={sample}"]
            if candidate["base"]:
                command.extend(["-base", str(candidate["base"])])
            command.extend([str(candidate["binary"]), str(candidate["path"])])
            try:
                result = subprocess.run(command, check=True, capture_output=True, text=True, encoding="utf-8", timeout=120, creationflags=getattr(subprocess, "CREATE_NO_WINDOW", 0))
                top.write_text(result.stdout, encoding="utf-8")
            except (subprocess.SubprocessError, OSError) as error:
                warnings.append(f"pprof failed for {run['name']} {sample}: {error}")
                continue
        if top is None:
            warnings.append(f"No existing full top table for {run['name']} {sample}; use --refresh-profile-tops after capture")
            continue
        try:
            values = parse_flat_top(top, sample)
        except (ValueError, UnicodeError) as error:
            warnings.append(f"Unusable top {top.name}: {error}")
            continue
        if values["hasNegativeSamples"]:
            warnings.append(f"Signed negative differences retained but excluded from share heatmap: {run['name']} {sample}")
        if values["total"] > 0 and values["listedFlat"] > values["total"] * 1.01:
            warnings.append(f"Flat sum exceeds reported total; inspect rounding/top source: {run['name']} {sample}")
        command = ["go", "tool", "pprof", "-http=127.0.0.1:0", f"-sample_index={sample}"]
        if candidate["base"]:
            command.extend(["-base", str(candidate["base"])])
        command.extend([str(candidate["binary"]), str(candidate["path"])])
        profiles.append({"run": run["name"], "status": run.get("status"), "revision": run.get("revision"), "sample": sample, "capturePhase": candidate["path"].stem, "profile": link(candidate["path"], figures.root), "base": link(candidate["base"], figures.root) if candidate["base"] else None, "top": link(top, figures.root), "binary": link(candidate["binary"], figures.root), "command": subprocess.list2cmdline(command), **values})
    for sample in PROFILE_SAMPLES:
        selected = [profile for profile in profiles if profile["sample"] == sample and profile["total"] > 0 and not profile["hasNegativeSamples"]]
        if not selected:
            continue
        categories = [name for name, _ in FLAT_CATEGORIES] + ["Unlisted / rounding"]
        shares = np.array([[100 * profile["categories"][name] / profile["total"] for name in categories] for profile in selected])
        figure, axis = plt.subplots(figsize=(19, max(5, len(selected) * 0.45 + 2)))
        chart = axis.imshow(shares, vmin=0, vmax=100, aspect="auto", cmap="YlOrRd")
        divisor, unit = (1, "CPU s") if sample == "cpu" else (MIB, "MiB") if sample.endswith("_space") else (1, "objects")
        for row, profile in enumerate(selected):
            for column, category in enumerate(categories):
                weight = profile["categories"][category]
                label = f"{format_value(weight / divisor)}\n{shares[row, column]:.1f}%"
                axis.text(column, row, label, ha="center", va="center", fontsize=7, color="white" if shares[row, column] > 60 else "#142238")
        axis.set_xticks(range(len(categories)), categories, rotation=35, ha="right")
        axis.set_yticks(range(len(selected)), [f"{profile['run']} [{profile['status']}]\n{profile['capturePhase']}; total {format_value(profile['total'] / divisor)} {unit}" for profile in selected], fontsize=8)
        axis.set_title(f"{figures.title} | {sample} FLAT samples only\nEach sample belongs to one leaf-function category; no parent/child cumulative sums\nColor = share of each whole profile; cells show {unit} and percentage")
        figure.colorbar(chart, ax=axis, label="Percent of the reported full-profile total")
        figure.tight_layout()
        figures.save(figure, "flat-hotspots-" + sample, sample + ": mutually exclusive flat-function categories", "hotspots")
    return profiles


def write_dashboard(root, capture, environment, groups, runs, profiles, figures, definitions, warnings):
    counts = Counter(run.get("status", "unknown") for run in runs)
    totals = {"scenarios": len(groups), "batches": sum(group["repeats"] for group in groups), "requests": sum(group["requests"] for group in groups), "runStatuses": dict(counts)}
    run_rows = [{"name": run["name"], "status": run.get("status", "incomplete"), "version": run.get("version"), "revision": run.get("revision"), "fixture": fixture_details(run), "code": run.get("code"), "elapsedMS": run.get("elapsedMS"), "error": run.get("error", run.get("reason")), "result": link(run["directory"] / "result.json", root) if (run["directory"] / "result.json").exists() else None, "measurements": link(run["directory"] / "measurements.json", root) if (run["directory"] / "measurements.json").exists() else None, "supplemental": [{"path": link(path, root), "name": path.name} for path in run.get("supplemental", [])], "log": link(Path(run["logPath"]), root) if run.get("logPath") else None} for run in runs]
    dataset = {"generatedAt": datetime.now(timezone.utc).isoformat(), "environment": environment, "counts": totals, "groups": groups, "runs": run_rows, "profiles": profiles, "figures": figures.items, "definitions": definitions, "warnings": warnings, "flatCategories": list(FLAT_CATEGORIES)}
    (root / "charts" / "data.json").write_text(json.dumps(dataset, ensure_ascii=False, indent=2, allow_nan=False), encoding="utf-8")
    data = json.dumps(dataset, ensure_ascii=False, allow_nan=False).replace("<", "\\u003c")
    repo = Path(__file__).resolve().parents[1]
    launcher = link(repo / "scripts" / "performance-review-open.mjs", root)
    title = html.escape(figures.title)
    page = '''<!doctype html>
<html lang="zh-CN"><meta charset="utf-8"><meta name="viewport" content="width=device-width">
<title>__TITLE__</title>
<style>
body{margin:0;background:#f3f6fa;color:#15243c;font:15px/1.6 system-ui,"Microsoft YaHei",sans-serif}main{max-width:1600px;margin:auto;padding:28px}h1{font-size:28px}h2{font-size:21px}a{color:#126a8a}section{background:white;padding:20px;margin:18px 0;border-radius:9px}nav{display:flex;gap:18px;flex-wrap:wrap}input,select{font:inherit;padding:8px;border:1px solid #9eadc0;border-radius:5px}input{min-width:280px}.scroll{overflow:auto;max-height:720px}table{width:100%;border-collapse:collapse;font-size:13px}th,td{padding:8px;border-bottom:1px solid #e2e8f0;text-align:left;vertical-align:top}th{position:sticky;top:0;background:#eaf1f7;white-space:nowrap}td small{display:block;color:#53667d}code,pre{white-space:pre-wrap;overflow-wrap:anywhere}img{max-width:100%;height:auto}.muted{color:#53667d}.warning{border-left:4px solid #bf7900;padding-left:12px}.failed,.timeout,.blocked,.incomplete{color:#a43a2f}details{margin:8px 0}.figure{margin:20px 0;border-top:1px solid #e2e8f0;padding-top:16px}button{font:inherit;padding:5px 12px}
</style><main>
<h1>__TITLE__</h1><p>本页只呈现当前捕获数据，不推断旧版本涨幅或项目总体倍数。CPU、完整响应消费延迟、累计分配、堆峰值和工作集具有不同含义。</p>
<nav><a href="REPORT.md">完整调查报告</a><a href="capture/flamegraphs.html">交互火焰图入口</a><a href="capture/summary.json">原始统计</a><a href="charts/data.json">可视化数据与分类规则</a><a href="capture/environment.json">测量环境</a><a href="__LAUNCHER__">本地 pprof 启动器源码</a></nav>
<section><h2>测量边界</h2><p id="counts"></p><pre id="environment"></pre><p>正式表统计 passed 且 Mode=measure 的已完成批次，排除 first-request 及 smoke/custom-smoke/sustain/timeline/trace 标签；包括关闭准备限流的 unrestricted 场景。CPU/内存诊断、取消和回收等样本保留在全部运行表。完整消费栏为每批延迟中位数的汇总；小样本不据此发布P95/P99。范围为所有已记录批次的最小值到最大值。</p><p>每20ms采样的绝对堆峰值可能漏掉尖峰，包含尚未回收垃圾；工作集包含Go堆之外的进程页。四个客户端不代表四个同时准备的请求；默认和关闭准备限流的曲线分开，实际准备活动及排队峰值范围直接显示在场景表。</p><details><summary>记录定义</summary><pre id="definitions"></pre></details><div id="warnings"></div></section>
<section><h2>场景成本</h2><input id="scenario-search" aria-label="筛选场景" placeholder="筛选场景、协议、日志或映射"><p class="muted">每格：中位数；下一行：完整范围。色图每列单独归一化，颜色不能跨指标当作绝对成本比较。</p><div class="scroll"><table><thead><tr><th>场景 / 版本</th><th>批次 / 请求数</th><th>CPU ms/请求</th><th>完整消费 ms</th><th>分配 MiB/请求</th><th>采样堆峰值 MiB</th><th>工作集 MiB</th><th>分配对象/请求</th></tr></thead><tbody id="scenarios"></tbody></table></div></section>
<section><h2>图表</h2><input id="figure-search" aria-label="筛选图表" placeholder="例如 hotspots、timeline、image、logprobs"><div id="figures"></div></section>
<section><h2>CPU / 分配 / 存活堆 flat 样本</h2><p>按叶函数的flat样本互斥归类；没有将父函数与子函数的cum相加。分类说明的是实际执行/分配位置，不能把所有runtime复制成本直接归于某一业务。存活堆是各采样时刻的分配来源，不能单凭此图判定保留者或泄漏。未列入top的质量单列unlisted；带负差分的profile仅留表，不伪造正占比。</p><input id="profile-search" aria-label="筛选profile" placeholder="筛选profile、cpu、alloc_space、inuse_space"><div id="profiles"></div></section>
<section><h2>全部运行与失败条件</h2><input id="run-search" aria-label="筛选运行" placeholder="筛选运行、失败原因"><select id="run-status" aria-label="运行状态"><option value="">全部状态</option></select><div class="scroll"><table><thead><tr><th>运行</th><th>状态</th><th>退出码 / elapsed ms</th><th>条件与原因</th><th>原始证据</th></tr></thead><tbody id="runs"></tbody></table></div></section>
<section><h2>验收限制</h2><p>所有图均来自本地合成负载。普通成功不替代实际触发规则、续传或取消的分支；Windows结果不替代Linux/macOS或真实供应商/客户端。运行失败、超时、环境阻塞和缺文件未被计作零成本或成功。PGO状态按捕获环境记录，本工具不更改构建。</p></section>
</main><script>
const data=__DATA__;
const esc=value=>String(value??'').replaceAll('&','&amp;').replaceAll('<','&lt;').replaceAll('>','&gt;').replaceAll('"','&quot;');
const fmt=value=>value===null||value===undefined?'—':Number(value).toLocaleString(undefined,{maximumFractionDigits:3});
const matches=(value,q)=>JSON.stringify(value).toLowerCase().includes(q.toLowerCase());
const anchor=(href,label)=>href?'<a href="'+esc(href)+'">'+esc(label)+'</a>':'';
document.querySelector('#counts').textContent=`${data.counts.scenarios}个正式场景/版本，${data.counts.batches}个批次，${data.counts.requests.toLocaleString()}个完整请求。全部运行状态：${JSON.stringify(data.counts.runStatuses)}`;
document.querySelector('#environment').textContent=JSON.stringify(data.environment,null,2);
document.querySelector('#definitions').textContent=JSON.stringify(data.definitions,null,2);
document.querySelector('#warnings').innerHTML=data.warnings.map(w=>'<p class="warning">'+esc(w)+'</p>').join('');
const metrics=[['cpuMS',1],['latencyMS',1],['bytes',1048576],['heapMiB',1],['rssMiB',1],['allocations',1]];
function drawScenarios(){const q=document.querySelector('#scenario-search').value;document.querySelector('#scenarios').innerHTML=data.groups.filter(g=>matches(g,q)).map(g=>{const p=g.preparationObserved,a=p?.activePeak,z=p?.queuedPeak;return '<tr><td>'+esc(g.name)+'<small>'+esc(g.version)+' · '+esc(g.revision)+'</small><small>客户端 '+fmt(g.fixture?.workers)+'；准备='+esc(g.fixture?.preparation)+'；活动峰值 '+fmt(a?.minimum)+'–'+fmt(a?.maximum)+'；排队峰值 '+fmt(z?.minimum)+'–'+fmt(z?.maximum)+'</small><details><summary>条件/逐批样本</summary><pre>'+esc(JSON.stringify({fixture:g.fixture,preparationObserved:p,samples:g.samples},null,2))+'</pre></details></td><td>'+g.repeats+' / '+g.requests+'</td>'+metrics.map(([k,d])=>{const m=g[k]||{};return '<td>'+fmt(m.median==null?null:m.median/d)+'<small>'+fmt(m.minimum==null?null:m.minimum/d)+' – '+fmt(m.maximum==null?null:m.maximum/d)+'</small></td>'}).join('')+'</tr>'}).join('')}
function drawFigures(){const q=document.querySelector('#figure-search').value;document.querySelector('#figures').innerHTML=data.figures.filter(f=>matches(f,q)).map(f=>'<div class="figure"><h3>'+esc(f.title)+'</h3>'+anchor(f.png,'PNG')+' · '+anchor(f.svg,'SVG')+'<br><a href="'+esc(f.png)+'"><img loading="lazy" src="'+esc(f.png)+'" alt="'+esc(f.title)+'"></a></div>').join('')}
function drawProfiles(){const q=document.querySelector('#profile-search').value;document.querySelector('#profiles').innerHTML=data.profiles.filter(p=>matches(p,q)).map(p=>'<details><summary>'+esc(p.run)+' · '+esc(p.capturePhase)+' · '+esc(p.sample)+' ['+esc(p.status)+']</summary><p>'+anchor(p.profile,'原始profile')+' · '+anchor(p.top,'完整/保留top')+' · '+anchor(p.binary,'匹配二进制')+(p.base?' · '+anchor(p.base,'差分基线profile'):'')+'</p><pre>'+esc(p.header)+'</pre><p>本地交互命令（选择Flame Graph）：</p><code>'+esc(p.command)+'</code><pre>'+esc(JSON.stringify({total:p.total,listedFlat:p.listedFlat,categories:p.categories,hasNegativeSamples:p.hasNegativeSamples},null,2))+'</pre></details>').join('')}
for(const status of Object.keys(data.counts.runStatuses).sort()){const option=document.createElement('option');option.value=status;option.textContent=status;document.querySelector('#run-status').append(option)}
function drawRuns(){const q=document.querySelector('#run-search').value,status=document.querySelector('#run-status').value;document.querySelector('#runs').innerHTML=data.runs.filter(r=>(!status||r.status===status)&&matches(r,q)).map(r=>'<tr><td>'+esc(r.name)+'</td><td class="'+esc(r.status)+'">'+esc(r.status)+'</td><td>'+esc(r.code)+' / '+fmt(r.elapsedMS)+'</td><td><details><summary>条件/错误</summary><pre>'+esc(JSON.stringify({fixture:r.fixture,error:r.error},null,2))+'</pre></details></td><td>'+[anchor(r.result,'result'),anchor(r.measurements,'measurements'),...(r.supplemental||[]).map(s=>anchor(s.path,s.name)),anchor(r.log,'log')].filter(Boolean).join(' · ')+'</td></tr>').join('')}
for(const [id,draw]of[['scenario-search',drawScenarios],['figure-search',drawFigures],['profile-search',drawProfiles],['run-search',drawRuns],['run-status',drawRuns]])document.getElementById(id).addEventListener('input',draw);
drawScenarios();drawFigures();drawProfiles();drawRuns();
</script></html>'''
    page = page.replace("__TITLE__", title).replace("__LAUNCHER__", html.escape(launcher)).replace("__DATA__", data)
    (root / "index.html").write_text(page, encoding="utf-8")
    return totals


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("root", nargs="?", default=DEFAULT_ROOT, type=Path)
    parser.add_argument("--refresh-profile-tops", action="store_true", help="Run local go tool pprof serially to retain all flat samples")
    arguments = parser.parse_args()
    root = arguments.root.resolve()
    capture = root / "capture"
    if not (capture / "runs").is_dir():
        parser.error(f"Missing capture/runs: {capture}")
    (root / "charts").mkdir(exist_ok=True)
    environment = read_json(capture / "environment.json", {})
    revision = str(environment.get("target", "unknown"))[:8]
    date = environment.get("date", "date unavailable")
    title = f"Elysia-Api {revision} | {date} | recorded current-version costs"
    plt.rcParams.update({"font.size": 9, "figure.dpi": 120, "savefig.dpi": 150, "axes.spines.top": False, "axes.spines.right": False})
    warnings = []
    figures = Figures(root, title, [])
    runs = load_runs(capture, warnings)
    groups, definitions = scenario_summaries(capture, runs, warnings)
    plot_cost_matrix(groups, figures)
    plot_scale_trends(groups, figures)
    plot_variants(groups, figures)
    plot_timelines(runs, figures, warnings)
    profiles = profile_hotspots(capture, runs, figures, arguments.refresh_profile_tops, warnings)
    totals = write_dashboard(root, capture, environment, groups, runs, profiles, figures, definitions, warnings)
    print(json.dumps({"root": str(root), "charts": len(figures.items), "profileTables": len(profiles), **totals, "warnings": len(warnings)}, ensure_ascii=False))


if __name__ == "__main__":
    main()
