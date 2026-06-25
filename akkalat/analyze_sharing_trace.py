#!/usr/bin/env python3
"""Analyze compressed page-sharing traces emitted by -trace-sharing."""

import argparse
import csv
import gzip
import math
from collections import Counter
from pathlib import Path


def parse_args():
    parser = argparse.ArgumentParser()
    parser.add_argument(
        "trace",
        nargs="+",
        help="One or more sharing trace files, usually *_sharing.csv.gz.",
    )
    parser.add_argument(
        "--out-dir",
        default="",
        help="Directory for derived CSV files. Defaults to each trace directory.",
    )
    parser.add_argument(
        "--head",
        type=int,
        default=10,
        help="Print the first N raw trace records before the summary.",
    )
    parser.add_argument(
        "--top-pages",
        type=int,
        default=20,
        help="Number of hot shared pages to export.",
    )
    parser.add_argument(
        "--log2-page-size",
        type=int,
        default=12,
        help="Used to derive page_id from vaddr when page_id is not in the trace.",
    )
    parser.add_argument(
        "--log2-cacheline-size",
        type=int,
        default=6,
        help="Used to derive cacheline_id from vaddr.",
    )
    parser.add_argument(
        "--cacheline-only",
        action="store_true",
        help="Only compute summary metrics needed for cacheline-sharing figures.",
    )
    parser.add_argument(
        "--remote-page-hotspot-only",
        action="store_true",
        help="Only compute and merge remote-page hotspot metrics into summary CSVs.",
    )
    return parser.parse_args()


def open_trace(path):
    if str(path).endswith(".gz"):
        return gzip.open(path, "rt", newline="")
    return open(path, "r", newline="")


def entropy_norm(counter):
    total = sum(counter.values())
    if total == 0 or len(counter) <= 1:
        return 0.0

    entropy = 0.0
    for value in counter.values():
        p = value / total
        entropy -= p * math.log2(p)
    return entropy / math.log2(len(counter))


def pct(value):
    if value is None:
        return "NA"
    return f"{100 * value:.2f}%"


def num(value):
    if value is None:
        return "NA"
    if isinstance(value, float):
        return f"{value:.6g}"
    return str(value)


def clean_stem(path):
    name = Path(path).name
    for suffix in (".csv.gz", ".gz", ".csv"):
        if name.endswith(suffix):
            return name[: -len(suffix)]
    return Path(path).stem


def write_metric_csv(path, metrics):
    with open(path, "w", newline="") as f:
        writer = csv.writer(f)
        writer.writerow(["metric", "value"])
        for key, value in metrics.items():
            writer.writerow([key, value])


def read_metric_csv(path):
    metrics = {}
    with open(path, newline="") as f:
        reader = csv.DictReader(f)
        for row in reader:
            value = row["value"]
            metrics[row["metric"]] = float(value) if value else None
    return metrics


CACHELINE_METRIC_KEYS = {
    "TotalCacheLines",
    "SharedCacheLines",
    "SharedCacheLineRatio",
    "SharedCacheLineByteRatio",
    "SharedCacheLineRemoteByteRatio",
    "WeightedCacheLineSharingDistance",
}


REMOTE_PAGE_HOTSPOT_KEYS = {
    "RemoteAccesses",
    "RemotePages",
    "RemotePagesPerRemoteAccess",
    "RemoteAccessesPerRemotePage",
    "RemoteBytesPerRemotePage",
}


def write_summary_metrics(path, metrics, cacheline_only=False):
    if not cacheline_only or not Path(path).exists():
        write_metric_csv(path, metrics)
        return

    merged_metrics = read_metric_csv(path)
    for key, value in metrics.items():
        if value is not None or key in CACHELINE_METRIC_KEYS:
            merged_metrics[key] = value
    write_metric_csv(path, merged_metrics)


def write_counter_csv(path, key_name, value_name, counter):
    with open(path, "w", newline="") as f:
        writer = csv.writer(f)
        writer.writerow([key_name, value_name])
        for key in sorted(counter):
            writer.writerow([key, counter[key]])


def new_access_stat():
    return {
        "bytes": 0,
        "accesses": 0,
        "remote_bytes": 0,
        "read_bytes": 0,
        "write_bytes": 0,
        "sharers": set(),
        "distance_bytes": Counter(),
    }


def update_access_stat(stat, requester, is_local, op, byte_count, distance):
    stat["bytes"] += byte_count
    stat["accesses"] += 1
    stat["remote_bytes"] += 0 if is_local else byte_count
    stat["read_bytes"] += byte_count if op == "R" else 0
    stat["write_bytes"] += byte_count if op == "W" else 0
    stat["sharers"].add(requester)
    if distance >= 0:
        stat["distance_bytes"][distance] += byte_count


def analyze_one_cacheline_only(trace_path, args):
    trace_path = Path(trace_path)
    total_accesses = 0
    total_bytes = 0
    local_accesses = 0
    remote_accesses = 0
    local_bytes = 0
    remote_bytes = 0
    read_bytes = 0
    write_bytes = 0
    bad_rows = 0

    cacheline_first_requester = {}
    shared_cachelines = set()
    remote_pages = set()
    distance_bytes = Counter()
    pair_bytes = Counter()

    with open_trace(trace_path) as f:
        reader = csv.reader(f)
        headers = next(reader, [])
        index = {name: i for i, name in enumerate(headers)}
        has_bytes = "bytes" in index
        pid_index = index.get("pid")
        op_index = index.get("op")
        bytes_index = index.get("bytes")

        try:
            requester_index = index["requester"]
            owner_index = index["owner"]
            vaddr_index = index["vaddr"]
            distance_index = index["distance"]
        except KeyError as err:
            raise SystemExit(f"{trace_path}: missing required column {err}") from err

        for row in reader:
            try:
                requester = int(row[requester_index])
                owner = int(row[owner_index])
                pid = int(row[pid_index]) if pid_index is not None else 0
                vaddr = int(row[vaddr_index])
                byte_count = int(row[bytes_index]) if has_bytes else 1
                distance = int(row[distance_index])
            except (IndexError, TypeError, ValueError):
                bad_rows += 1
                continue

            is_local = requester == owner
            page_id = vaddr >> args.log2_page_size
            page_key = (pid, page_id) if pid_index is not None else page_id
            total_accesses += 1
            total_bytes += byte_count
            if is_local:
                local_accesses += 1
                local_bytes += byte_count
            else:
                remote_accesses += 1
                remote_bytes += byte_count
                remote_pages.add(page_key)

            if op_index is not None:
                op = row[op_index]
                if op == "R":
                    read_bytes += byte_count
                elif op == "W":
                    write_bytes += byte_count

            if distance >= 0:
                distance_bytes[distance] += byte_count

            pair_bytes[(requester, owner)] += byte_count

            cacheline_id = vaddr >> args.log2_cacheline_size
            cacheline_key = (pid, cacheline_id) if pid_index is not None else cacheline_id
            first_requester = cacheline_first_requester.get(cacheline_key)
            if first_requester is None:
                cacheline_first_requester[cacheline_key] = requester
            elif first_requester != requester:
                shared_cachelines.add(cacheline_key)

    (
        shared_cacheline_bytes,
        shared_cacheline_remote_bytes,
        shared_cacheline_distance_num,
        shared_cacheline_distance_total_bytes,
    ) = collect_shared_cacheline_traffic_fast(trace_path, args, shared_cachelines)

    weighted_distance_num = sum(
        distance * byte_count for distance, byte_count in distance_bytes.items()
    )
    distance_total_bytes = sum(distance_bytes.values())
    pair_entropy = entropy_norm(pair_bytes)
    distance_entropy = entropy_norm(distance_bytes)
    dominant_pair_ratio = (
        max(pair_bytes.values()) / total_bytes if total_bytes else None
    )

    metrics = {
        "TotalAccesses": total_accesses,
        "TotalBytes": total_bytes,
        "LocalAccessRatio": local_accesses / total_accesses
        if total_accesses
        else None,
        "RemoteAccessRatio": remote_accesses / total_accesses
        if total_accesses
        else None,
        "RemoteAccesses": remote_accesses,
        "RemotePages": len(remote_pages),
        "RemotePagesPerRemoteAccess": len(remote_pages) / remote_accesses
        if remote_accesses
        else None,
        "RemoteAccessesPerRemotePage": remote_accesses / len(remote_pages)
        if remote_pages
        else None,
        "RemoteBytesPerRemotePage": remote_bytes / len(remote_pages)
        if remote_pages
        else None,
        "LocalByteRatio": local_bytes / total_bytes if total_bytes else None,
        "RemoteByteRatio": remote_bytes / total_bytes if total_bytes else None,
        "ReadByteRatio": read_bytes / total_bytes if total_bytes else None,
        "WriteByteRatio": write_bytes / total_bytes if total_bytes else None,
        "TotalCacheLines": len(cacheline_first_requester),
        "SharedCacheLines": len(shared_cachelines),
        "SharedCacheLineRatio": len(shared_cachelines)
        / len(cacheline_first_requester)
        if cacheline_first_requester
        else None,
        "SharedCacheLineByteRatio": shared_cacheline_bytes / total_bytes
        if total_bytes
        else None,
        "SharedCacheLineRemoteByteRatio": shared_cacheline_remote_bytes / total_bytes
        if total_bytes
        else None,
        "WeightedAccessDistance": weighted_distance_num / distance_total_bytes
        if distance_total_bytes
        else None,
        "WeightedCacheLineSharingDistance": shared_cacheline_distance_num
        / shared_cacheline_distance_total_bytes
        if shared_cacheline_distance_total_bytes
        else None,
        "PairEntropyNorm": pair_entropy,
        "DistanceEntropyNorm": distance_entropy,
        "RegularityScore": 1.0 - pair_entropy,
        "DominantPairByteRatio": dominant_pair_ratio,
        "Top1SharedPageByteRatio": None,
        "Top10SharedPageByteRatio": None,
        "BadRows": bad_rows,
    }

    out_dir = Path(args.out_dir) if args.out_dir else trace_path.parent
    out_dir.mkdir(parents=True, exist_ok=True)
    prefix = out_dir / clean_stem(trace_path)
    write_summary_metrics(f"{prefix}_summary_metrics.csv", metrics, cacheline_only=True)
    print_report(trace_path, headers, [], metrics, has_bytes)


def analyze_one_remote_page_hotspot_only(trace_path, args):
    trace_path = Path(trace_path)
    remote_accesses = 0
    remote_bytes = 0
    remote_pages = set()
    bad_rows = 0

    with open_trace(trace_path) as f:
        reader = csv.reader(f)
        headers = next(reader, [])
        index = {name: i for i, name in enumerate(headers)}
        has_bytes = "bytes" in index
        pid_index = index.get("pid")
        bytes_index = index.get("bytes")

        try:
            requester_index = index["requester"]
            owner_index = index["owner"]
            vaddr_index = index["vaddr"]
        except KeyError as err:
            raise SystemExit(f"{trace_path}: missing required column {err}") from err

        for row in reader:
            try:
                requester = int(row[requester_index])
                owner = int(row[owner_index])
                pid = int(row[pid_index]) if pid_index is not None else 0
                vaddr = int(row[vaddr_index])
                byte_count = int(row[bytes_index]) if has_bytes else 1
            except (IndexError, TypeError, ValueError):
                bad_rows += 1
                continue

            if requester == owner:
                continue

            page_id = vaddr >> args.log2_page_size
            page_key = (pid, page_id) if pid_index is not None else page_id
            remote_accesses += 1
            remote_bytes += byte_count
            remote_pages.add(page_key)

    metrics = {
        "RemoteAccesses": remote_accesses,
        "RemotePages": len(remote_pages),
        "RemotePagesPerRemoteAccess": len(remote_pages) / remote_accesses
        if remote_accesses
        else None,
        "RemoteAccessesPerRemotePage": remote_accesses / len(remote_pages)
        if remote_pages
        else None,
        "RemoteBytesPerRemotePage": remote_bytes / len(remote_pages)
        if remote_pages
        else None,
        "RemotePageHotspotBadRows": bad_rows,
    }

    out_dir = Path(args.out_dir) if args.out_dir else trace_path.parent
    out_dir.mkdir(parents=True, exist_ok=True)
    prefix = out_dir / clean_stem(trace_path)
    summary_path = f"{prefix}_summary_metrics.csv"
    merged_metrics = read_metric_csv(summary_path) if Path(summary_path).exists() else {}
    merged_metrics.update(metrics)
    write_metric_csv(summary_path, merged_metrics)

    print(f"{trace_path}: RemoteAccesses={remote_accesses}, RemotePages={len(remote_pages)}")


def collect_shared_cacheline_traffic_fast(trace_path, args, shared_cachelines):
    if not shared_cachelines:
        return 0, 0, 0, 0

    shared_bytes = 0
    shared_remote_bytes = 0
    shared_distance_num = 0
    shared_distance_total_bytes = 0

    with open_trace(trace_path) as f:
        reader = csv.reader(f)
        headers = next(reader, [])
        index = {name: i for i, name in enumerate(headers)}
        has_bytes = "bytes" in index
        pid_index = index.get("pid")
        bytes_index = index.get("bytes")
        requester_index = index["requester"]
        owner_index = index["owner"]
        vaddr_index = index["vaddr"]
        distance_index = index["distance"]

        for row in reader:
            try:
                requester = int(row[requester_index])
                owner = int(row[owner_index])
                pid = int(row[pid_index]) if pid_index is not None else 0
                vaddr = int(row[vaddr_index])
                byte_count = int(row[bytes_index]) if has_bytes else 1
                distance = int(row[distance_index])
            except (IndexError, TypeError, ValueError):
                continue

            cacheline_id = vaddr >> args.log2_cacheline_size
            cacheline_key = (pid, cacheline_id) if pid_index is not None else cacheline_id
            if cacheline_key not in shared_cachelines:
                continue

            shared_bytes += byte_count
            if requester != owner:
                shared_remote_bytes += byte_count
            if distance >= 0:
                shared_distance_num += distance * byte_count
                shared_distance_total_bytes += byte_count

    return (
        shared_bytes,
        shared_remote_bytes,
        shared_distance_num,
        shared_distance_total_bytes,
    )


def analyze_one(trace_path, args):
    if args.remote_page_hotspot_only:
        analyze_one_remote_page_hotspot_only(trace_path, args)
        return
    if args.cacheline_only:
        analyze_one_cacheline_only(trace_path, args)
        return

    trace_path = Path(trace_path)
    raw_rows = []
    total_accesses = 0
    total_bytes = 0
    local_accesses = 0
    remote_accesses = 0
    local_bytes = 0
    remote_bytes = 0
    read_bytes = 0
    write_bytes = 0
    bad_rows = 0
    has_bytes = False

    pages = {}
    remote_pages = set()
    cacheline_first_requester = {}
    shared_cachelines = set()
    distance_bytes = Counter()
    distance_accesses = Counter()
    pair_bytes = Counter()
    pair_accesses = Counter()
    owner_bytes = Counter()
    requester_bytes = Counter()

    with open_trace(trace_path) as f:
        reader = csv.DictReader(f)
        headers = reader.fieldnames or []
        has_bytes = "bytes" in headers
        for row in reader:
            if len(raw_rows) < args.head:
                raw_rows.append(row)

            try:
                requester = int(row["requester"])
                owner = int(row["owner"])
                pid = int(row["pid"]) if "pid" in row else 0
                vaddr = int(row["vaddr"])
                page_id = (
                    int(row["page_id"])
                    if "page_id" in row
                    else vaddr >> args.log2_page_size
                )
                cacheline_id = vaddr >> args.log2_cacheline_size
                op = row["op"]
                byte_count = int(row["bytes"]) if has_bytes else 1
                distance = int(row.get("distance", -1))
            except (KeyError, TypeError, ValueError):
                bad_rows += 1
                continue

            is_local = requester == owner
            page_key = (pid, page_id)
            cacheline_key = (pid, cacheline_id)
            pair_key = (requester, owner)

            total_accesses += 1
            total_bytes += byte_count
            if is_local:
                local_accesses += 1
                local_bytes += byte_count
            else:
                remote_accesses += 1
                remote_bytes += byte_count
                remote_pages.add(page_key)

            if op == "R":
                read_bytes += byte_count
            elif op == "W":
                write_bytes += byte_count

            if distance >= 0:
                distance_bytes[distance] += byte_count
                distance_accesses[distance] += 1

            pair_bytes[pair_key] += byte_count
            pair_accesses[pair_key] += 1
            owner_bytes[owner] += byte_count
            requester_bytes[requester] += byte_count

            update_access_stat(
                pages.setdefault(page_key, new_access_stat()),
                requester,
                is_local,
                op,
                byte_count,
                distance,
            )

            first_requester = cacheline_first_requester.get(cacheline_key)
            if first_requester is None:
                cacheline_first_requester[cacheline_key] = requester
            elif first_requester != requester:
                shared_cachelines.add(cacheline_key)

    shared_pages = {
        page: stat for page, stat in pages.items() if len(stat["sharers"]) >= 2
    }
    shared_bytes = sum(stat["bytes"] for stat in shared_pages.values())
    shared_remote_bytes = sum(stat["remote_bytes"] for stat in shared_pages.values())

    shared_distance_bytes = Counter()
    for stat in shared_pages.values():
        shared_distance_bytes.update(stat["distance_bytes"])

    sharer_count_dist = Counter(len(stat["sharers"]) for stat in pages.values())
    shared_sharer_count_dist = Counter(
        len(stat["sharers"]) for stat in shared_pages.values()
    )

    top_shared_pages = sorted(
        shared_pages.items(),
        key=lambda item: item[1]["bytes"],
        reverse=True,
    )

    (
        shared_cacheline_bytes,
        shared_cacheline_remote_bytes,
        shared_cacheline_distance_num,
        shared_cacheline_distance_total_bytes,
    ) = collect_shared_cacheline_traffic_fast(trace_path, args, shared_cachelines)

    weighted_distance_num = sum(
        distance * byte_count for distance, byte_count in distance_bytes.items()
    )
    weighted_sharing_distance_num = sum(
        distance * byte_count for distance, byte_count in shared_distance_bytes.items()
    )
    distance_total_bytes = sum(distance_bytes.values())
    shared_distance_total_bytes = sum(shared_distance_bytes.values())

    pair_entropy = entropy_norm(pair_bytes)
    distance_entropy = entropy_norm(distance_bytes)
    dominant_pair_ratio = (
        max(pair_bytes.values()) / total_bytes if total_bytes else None
    )
    regularity_score = 1.0 - pair_entropy

    top1_shared_page_ratio = (
        top_shared_pages[0][1]["bytes"] / total_bytes
        if total_bytes and top_shared_pages
        else None
    )
    top10_shared_page_ratio = (
        sum(stat["bytes"] for _, stat in top_shared_pages[:10]) / total_bytes
        if total_bytes and top_shared_pages
        else None
    )

    metrics = {
        "TotalAccesses": total_accesses,
        "TotalBytes": total_bytes,
        "LocalAccessRatio": local_accesses / total_accesses
        if total_accesses
        else None,
        "RemoteAccessRatio": remote_accesses / total_accesses
        if total_accesses
        else None,
        "RemoteAccesses": remote_accesses,
        "RemotePages": len(remote_pages),
        "RemotePagesPerRemoteAccess": len(remote_pages) / remote_accesses
        if remote_accesses
        else None,
        "RemoteAccessesPerRemotePage": remote_accesses / len(remote_pages)
        if remote_pages
        else None,
        "RemoteBytesPerRemotePage": remote_bytes / len(remote_pages)
        if remote_pages
        else None,
        "LocalByteRatio": local_bytes / total_bytes if total_bytes else None,
        "RemoteByteRatio": remote_bytes / total_bytes if total_bytes else None,
        "ReadByteRatio": read_bytes / total_bytes if total_bytes else None,
        "WriteByteRatio": write_bytes / total_bytes if total_bytes else None,
        "TotalPages": len(pages),
        "SharedPages": len(shared_pages),
        "SharedPageRatio": len(shared_pages) / len(pages) if pages else None,
        "SharedByteRatio": shared_bytes / total_bytes if total_bytes else None,
        "SharedRemoteByteRatio": shared_remote_bytes / total_bytes
        if total_bytes
        else None,
        "TotalCacheLines": len(cacheline_first_requester),
        "SharedCacheLines": len(shared_cachelines),
        "SharedCacheLineRatio": len(shared_cachelines)
        / len(cacheline_first_requester)
        if cacheline_first_requester
        else None,
        "SharedCacheLineByteRatio": shared_cacheline_bytes / total_bytes
        if total_bytes
        else None,
        "SharedCacheLineRemoteByteRatio": shared_cacheline_remote_bytes / total_bytes
        if total_bytes
        else None,
        "WeightedAccessDistance": weighted_distance_num / distance_total_bytes
        if distance_total_bytes
        else None,
        "WeightedSharingDistance": weighted_sharing_distance_num
        / shared_distance_total_bytes
        if shared_distance_total_bytes
        else None,
        "WeightedCacheLineSharingDistance": shared_cacheline_distance_num
        / shared_cacheline_distance_total_bytes
        if shared_cacheline_distance_total_bytes
        else None,
        "PairEntropyNorm": pair_entropy,
        "DistanceEntropyNorm": distance_entropy,
        "RegularityScore": regularity_score,
        "DominantPairByteRatio": dominant_pair_ratio,
        "Top1SharedPageByteRatio": top1_shared_page_ratio,
        "Top10SharedPageByteRatio": top10_shared_page_ratio,
        "BadRows": bad_rows,
    }

    out_dir = Path(args.out_dir) if args.out_dir else trace_path.parent
    out_dir.mkdir(parents=True, exist_ok=True)
    prefix = out_dir / clean_stem(trace_path)

    write_summary_metrics(f"{prefix}_summary_metrics.csv", metrics)
    write_counter_csv(
        f"{prefix}_sharer_count_distribution.csv",
        "sharer_count",
        "num_pages",
        sharer_count_dist,
    )
    write_counter_csv(
        f"{prefix}_shared_sharer_count_distribution.csv",
        "sharer_count",
        "num_shared_pages",
        shared_sharer_count_dist,
    )
    write_counter_csv(
        f"{prefix}_shared_bytes_by_distance.csv",
        "distance",
        "bytes",
        shared_distance_bytes,
    )
    write_counter_csv(
        f"{prefix}_all_bytes_by_distance.csv",
        "distance",
        "bytes",
        distance_bytes,
    )

    with open(f"{prefix}_top_shared_pages.csv", "w", newline="") as f:
        writer = csv.writer(f)
        writer.writerow(
            [
                "pid",
                "page_id",
                "bytes",
                "accesses",
                "remote_bytes",
                "sharer_count",
                "read_bytes",
                "write_bytes",
            ]
        )
        for (pid, page_id), stat in top_shared_pages[: args.top_pages]:
            writer.writerow(
                [
                    pid,
                    page_id,
                    stat["bytes"],
                    stat["accesses"],
                    stat["remote_bytes"],
                    len(stat["sharers"]),
                    stat["read_bytes"],
                    stat["write_bytes"],
                ]
            )

    with open(f"{prefix}_pair_bytes.csv", "w", newline="") as f:
        writer = csv.writer(f)
        writer.writerow(["requester", "owner", "bytes", "accesses"])
        for (requester, owner), byte_count in sorted(pair_bytes.items()):
            writer.writerow(
                [requester, owner, byte_count, pair_accesses[(requester, owner)]]
            )

    print_report(trace_path, headers, raw_rows, metrics, has_bytes)


def print_report(trace_path, headers, raw_rows, metrics, has_bytes):
    print(f"\n=== 分析 trace: {trace_path} ===")
    if raw_rows:
        print("\n[原始 trace 样例]")
        print("\t".join(headers))
        for row in raw_rows:
            print("\t".join(row.get(h, "") for h in headers))

    print("\n[核心指标]")
    for key in (
        "TotalAccesses",
        "TotalBytes",
        "LocalAccessRatio",
        "RemoteAccessRatio",
        "SharedCacheLineRatio",
        "SharedCacheLineByteRatio",
        "WeightedCacheLineSharingDistance",
        "PairEntropyNorm",
        "RegularityScore",
        "DominantPairByteRatio",
        "Top1SharedPageByteRatio",
        "Top10SharedPageByteRatio",
        "BadRows",
    ):
        value = metrics[key]
        if key.endswith("Ratio") or key in ("RegularityScore", "PairEntropyNorm"):
            print(f"{key}: {pct(value)}")
        else:
            print(f"{key}: {num(value)}")

    if not has_bytes:
        print("\n[说明]")
        print("- trace 中没有 bytes 字段，带 Byte/Bytes 的指标按每行访问权重 1 统计。")

    print("\n[可能观察]")
    remote = metrics["RemoteAccessRatio"] or 0.0
    shared = metrics["SharedCacheLineByteRatio"] or 0.0
    regularity = metrics["RegularityScore"] or 0.0
    top10 = metrics["Top10SharedPageByteRatio"] or 0.0

    if shared >= 0.30 and remote >= 0.30:
        print("- 共享字节和远程访问都较高：该 workload 可能存在“有共享但 locality 差”的优化空间。")
    elif remote <= 0.10:
        print("- 远程访问比例较低：当前映射下大部分数据已经本地化。")
    elif shared >= 0.30:
        print("- 共享数据较多，但远程比例不一定高：需要区分共享本身和共享导致的跨 tile 流量。")
    else:
        print("- 共享占比不高：数据共享优化可能只应针对少数热点页。")

    if regularity >= 0.60:
        print("- requester-owner pair 分布集中：共享模式较规则，适合用简单统计图展示。")
    else:
        print("- requester-owner pair 分布分散：共享模式较不规则，建议画热力图或熵指标。")

    if top10 >= 0.30:
        print("- Top shared pages 承载大量字节：可能存在少数热点共享页。")


def main():
    args = parse_args()
    for trace in args.trace:
        analyze_one(trace, args)


if __name__ == "__main__":
    main()
