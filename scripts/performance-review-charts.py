"""Plot recorded measurements only; requires matplotlib in a disposable venv."""
import json
import sys
from pathlib import Path

import matplotlib
matplotlib.use("Agg")
import matplotlib.pyplot as plt
import numpy as np

root = Path(sys.argv[1] if len(sys.argv) > 1 else ".tmp-dev/performance-review-2026-10-22")
out = root / "charts"
out.mkdir(exist_ok=True)
data = json.loads((root / "summary.json").read_text(encoding="utf-8"))
summaries = {r["name"]: r for r in data["summaries"]}
colors = {"release": "#9b8d79", "merged": "#829ab1", "normalized": "#d69e2e", "current": "#087f8c"}
plt.rcParams.update({"font.size": 10, "figure.dpi": 130, "savefig.dpi": 160, "axes.spines.top": False, "axes.spines.right": False})

def save(fig, name):
    fig.savefig(out / f"{name}.png", bbox_inches="tight")
    fig.savefig(out / f"{name}.svg", bbox_inches="tight")
    plt.close(fig)

for name, selection in {
    "ordinary": ["main-chat-short", "main-anthropic-short", "main-responses-short", "main-gemini-short", "main-history256k", "main-candidates4", "extended-candidates16", "main-stream256-cross", "extended-nodes", "extended-logprobs"],
    "large": ["main-image2", "extended-image8", "extended-image16", "extended-image31", "extended-image31-sse", "extended-image31-four", "unrestricted-image31-four", "extended-image31-body", "extended-image31-media"],
}.items():
    selection = [k for k in selection if k in summaries]
    fig, axes = plt.subplots(2, 2, figsize=(17, 11))
    for ax, metric, divisor, unit in zip(axes.flat, ["bytes", "cpuMS", "heapMiB", "rssMiB"], [2**20, 1, 1, 1], ["Cumulative allocation / request (MiB)", "Gateway CPU / request (ms)", "Sampled absolute heap peak (MiB)", "Sampled process working-set peak (MiB)"]):
        for i, version in enumerate(["release", "current"]):
            for j, key in enumerate(selection):
                v = summaries[key]["versions"].get(version)
                if not v:
                    continue
                value = v[metric]
                x = j + (i - .5) * .38
                ax.bar(x, value["median"] / divisor, width=.35, color=colors[version], label=version if j == 0 else None)
                ax.errorbar(x, value["median"] / divisor, yerr=[[max(0, value["median"]-value["minimum"])/divisor], [max(0, value["maximum"]-value["median"])/divisor]], color="#283044", capsize=3, linewidth=1)
        ax.set_xticks(range(len(selection)), [s.replace("main-", "").replace("extended-", "") for s in selection], rotation=40, ha="right")
        ax.set_title(unit)
        ax.grid(axis="y", alpha=.2)
        ax.legend()
    fig.suptitle(f"v1.6.2 vs 760b54af | Windows / Go 1.27.2 / GOMAXPROCS=4\n{name}: median and full range of 5 alternating pairs; no weighted overall score", fontsize=14)
    fig.tight_layout(rect=(0, 0, 1, .93))
    save(fig, name)

latest = [x for x in summaries if x.startswith("latest-")]
if latest:
    fig, axes = plt.subplots(2, 2, figsize=(16, 11))
    for ax, metric, factor, unit in zip(axes.flat, ["bytes", "cpuMS", "latencyMS", "rssMiB"], [1024, 1, 1, 1], ["KiB allocated / request", "CPU ms / request", "Full-consumption latency median (ms)", "Working-set peak (MiB)"]):
        for i, version in enumerate(["merged", "normalized", "current"]):
            values = [summaries[k]["versions"].get(version, {}).get(metric, {}).get("median", float("nan"))/factor for k in latest]
            ax.bar(np.arange(len(latest))+(i-1)*.25, values, width=.24, label=version, color=colors[version])
        ax.set_title(unit); ax.legend(); ax.set_xticks(range(len(latest)), [x.replace("latest-", "") for x in latest], rotation=40, ha="right"); ax.grid(axis="y", alpha=.2)
    fig.suptitle("ba6f8563 → 93509b13 → 760b54af | separate normalization from Hertz integration")
    fig.tight_layout(rect=(0, 0, 1, .95)); save(fig, "latest")

selection = [s for s in summaries if s.startswith(("main-", "extended-"))]
metrics = ["cpuMS", "latencyMS", "bytes", "heapMiB", "rssMiB"]
ratios = np.array([[summaries[k]["versions"]["current"][m]["median"] / summaries[k]["versions"]["release"][m]["median"] if min(summaries[k]["versions"]["release"][m]["median"], summaries[k]["versions"]["current"][m]["median"]) > 0 else np.nan for m in metrics] for k in selection])
fig, ax = plt.subplots(figsize=(12, max(10, len(selection)*.35)))
im = ax.imshow(np.log2(ratios), cmap="RdYlGn_r", vmin=-4, vmax=4, aspect="auto")
for row in range(len(selection)):
    for col in range(len(metrics)):
        ax.text(col, row, f"{100*(ratios[row,col]-1):+.1f}%" if np.isfinite(ratios[row,col]) else "resolution", ha="center", va="center", fontsize=9)
ax.set_xticks(range(len(metrics)), ["CPU/request", "Full latency P50", "Allocation/request", "Heap peak", "Working-set peak"])
ax.set_yticks(range(len(selection)), selection)
ax.set_title("v1.6.2 → 760b54af | ratio of five-run medians\nNegative = lower cost; every workload has its own denominator")
fig.colorbar(im, ax=ax, label="log2(current / release); color clipped at ±4")
fig.tight_layout(); save(fig, "cost-changes")

# First HTTP calls are after fixture initialization, not process-start time.
cold = json.loads((root / "cold-and-reload.json").read_text(encoding="utf-8"))["cold"]
cold_cases = ["chat-short", "anthropic-short", "responses-short", "gemini-short", "candidates4"]
fig, axes = plt.subplots(1, 2, figsize=(13, 5))
for i, version in enumerate(["release", "current"]):
    for j, case in enumerate(cold_cases):
        rows = [r["measurement"] for r in cold if r["version"] == version and r["run"].startswith(f"main-{case}-")]
        if not rows:
            continue
        for ax, values, unit in [(axes[0], [r["ElapsedMS"] for r in rows], "First HTTP elapsed (ms)"), (axes[1], [r["Metrics"]["Allocated"]/2**20 for r in rows], "First HTTP allocation (MiB)")]:
            ax.bar(j+(i-.5)*.38, np.median(values), width=.35, color=colors[version], label=version if j == 0 else None)
            ax.set_title(unit)
for ax in axes:
    ax.set_xticks(range(len(cold_cases)), cold_cases, rotation=30, ha="right"); ax.legend(); ax.grid(axis="y", alpha=.2)
fig.suptitle("Cold first call | 5 independent processes per version | excludes fixture initialization")
fig.tight_layout(rect=(0,0,1,.93)); save(fig, "cold-first-call")

for key in ["main-chat-short", "extended-nodes", "extended-image31", "extended-image31-four", "unrestricted-image31-four", "timeline-image31-four", "timeline-off-image31-four", "main-stream256-cross"]:
    fig, axes = plt.subplots(4, 2, figsize=(15, 12))
    available = False
    for col, version in enumerate(["release", "current"]):
        file = root / "runs" / f"{key}-1-{version}" / "measurements.json"
        if not file.exists():
            continue
        row = next((r for r in json.loads(file.read_text()) if r["Phase"] != "first-request"), None)
        samples = row["Metrics"].get("Timeline", []) if row else []
        if not samples:
            continue
        available = True
        x = np.array([r["ElapsedMS"] for r in samples])/1000
        for field, label in [("Heap", "HeapAlloc"), ("HeapInuse", "HeapInuse"), ("RSS", "Working set")]:
            axes[0,col].plot(x, [r[field]/2**20 for r in samples], label=label)
        axes[0,col].set_ylabel("MiB"); axes[0,col].set_title(version); axes[0,col].legend()
        axes[1,col].step(x, [r["ActiveRequests"] for r in samples], where="post", label="Active HTTP")
        if "PreparationActive" in samples[0] and version == "current":
            for field in ["PreparationActive", "PreparationQueued"]:
                axes[1,col].step(x, [r[field] for r in samples], where="post", label=field)
        axes[1,col].set_ylabel("Requests"); axes[1,col].legend()
        if "GCCycles" in samples[0]:
            axes[2,col].step(x, [r["GCCycles"]-samples[0]["GCCycles"] for r in samples], label="GC cycles", where="post")
            axes[3,col].plot(x, [(r["GCPauseNS"]-samples[0]["GCPauseNS"])/1e6 for r in samples], label="Accumulated GC STW pause")
            axes[2,col].set_ylabel("GC cycles"); axes[3,col].set_ylabel("Pause ms")
        else:
            axes[2,col].text(.05,.5,"Per-sample GC / preparation unavailable\nin original collector; see supplemental timeline", transform=axes[2,col].transAxes)
            axes[3,col].axis("off")
        for ax in axes[:,col]:
            ax.grid(alpha=.2); ax.set_xlabel("Seconds since measurement reset")
    if available:
        fig.suptitle(f"{key} | predetermined pair 1 | 20 ms sampling, not exact peaks\nIdle tail is natural recovery; no forced GC in these formal runs")
        fig.tight_layout(rect=(0,0,1,.95)); save(fig, key + "-timeline")
    else:
        plt.close(fig)
probability_cases = ["extended-logprobs", "supplemental-logprobs1024", "cross-control-responses-chat-stream256", "cross-control-responses-chat-stream1024", "cross-logprobs-responses", "cross-logprobs-responses1024"]
probability_cases = [key for key in probability_cases if key in summaries]
if probability_cases:
    fig, axes = plt.subplots(1, 3, figsize=(18, 6))
    for ax, metric, divisor, unit in zip(axes, ["bytes", "cpuMS", "latencyMS"], [2**20, 1, 1], ["Allocated MiB / request", "CPU ms / request", "Full consumption ms"]):
        for i, version in enumerate(["release", "current"]):
            values = [summaries[k]["versions"][version][metric]["median"]/divisor for k in probability_cases]
            ax.bar(np.arange(len(values))+(i-.5)*.38, values, width=.35, color=colors[version], label=version)
        ax.set_xticks(range(len(probability_cases)), [k.replace("cross-control-responses-chat-stream", "cross plain ").replace("cross-logprobs-responses", "cross prob ").replace("extended-logprobs", "native prob 256").replace("supplemental-logprobs", "native prob ") for k in probability_cases], rotation=45, ha="right")
        ax.set_title(unit); ax.legend(); ax.grid(axis="y", alpha=.2)
    fig.suptitle("Probability stream growth | 256 vs 1024 frames | actual HTTP paths")
    fig.tight_layout(rect=(0,0,1,.93)); save(fig, "probability-streams")

ledger_files = [root / "ledger" / f"ledger-{size}.json" for size in [2,31]]
if all(file.exists() for file in ledger_files):
    fig, axes = plt.subplots(1, 2, figsize=(14, 5))
    phases = ["decode", "convert", "encode", "validateWire"]
    for i, size in enumerate([2,31]):
        ledger = json.loads(ledger_files[i].read_text(encoding="utf-8"))["false"]
        for ax, metric, title in zip(axes, ["meter.scanned", "stringEnd.traversed"], ["Logical spans entering scanRaw / payload size", "String-boundary traversed spans / payload size"]):
            values = [ledger[p].get(metric, {}).get("Bytes", 0)/(size*2**20) for p in phases]
            ax.bar(np.arange(len(phases))+(i-.5)*.38, values, width=.35, label=f"{size} MiB")
            ax.set_xticks(range(len(phases)), phases); ax.set_title(title); ax.legend(); ax.grid(axis="y", alpha=.2)
    fig.suptitle("Instrumented isolated request | logical spans, NOT allocation or RAM-bus traffic\nOverlapping counters must not be added; public byte API differs from gateway Value handoff")
    fig.tight_layout(rect=(0,0,1,.86)); save(fig,"processing-ledger")

retention_file = root / "retention.json"
if retention_file.exists():
    retention = json.loads(retention_file.read_text(encoding="utf-8"))
    cases = sorted({r["run"].rsplit("-0-", 1)[0] for r in retention["sustained"]})
    if cases:
        fig, axes = plt.subplots(len(cases), 2, figsize=(14, 4*len(cases)), squeeze=False)
        for row, key in enumerate(cases):
            for run in retention["sustained"]:
                if run["run"].rsplit("-0-", 1)[0] != key:
                    continue
                for col, metric in enumerate(["heapMiB", "workingSetMiB"]):
                    values = [r["idle"][-1][metric] for r in run["rounds"] if r.get("idle")]
                    axes[row,col].plot(range(1,len(values)+1), values, marker="o", color=colors[run["version"]], label=run["version"])
                    axes[row,col].set_title(key+" / "+metric); axes[row,col].set_xlabel("Round in same process"); axes[row,col].set_ylabel("MiB after natural idle"); axes[row,col].grid(alpha=.2); axes[row,col].legend()
        fig.suptitle("Natural recovery across repeated rounds | no forced GC | not a long-term leak proof")
        fig.tight_layout(rect=(0,0,1,.96)); save(fig, "natural-recovery")
print(out)
