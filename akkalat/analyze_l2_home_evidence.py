#!/usr/bin/env python3
"""Join page-sharing traces with L2-source stats.

The output is intended to test a "home L2" hypothesis:
some read-mostly pages may be reused by several requester GPUs, so keeping the
data hot in one provider GPU's L2 could serve peer GPUs without repeated DRAM
fills.
"""

import argparse
import csv
from collections import defaultdict
from pathlib import Path


SOURCE_COLUMNS = [
    "local_l2_cache_read",
    "local_l2_mshr_read",
    "local_dram_read",
    "remote_l2_cache_read",
    "remote_l2_mshr_read",
    "remote_dram_read",
    "remote_write_allocate_write",
    "remote_neighbor_read",
]


def parse_args():
    parser = argparse.ArgumentParser()
    parser.add_argument(
        "result_dir",
        help="Result directory containing *_sharing_pages.csv and L2-source CSVs.",
    )
    parser.add_argument(
        "--page-size",
        type=int,
        default=4096,
        help="Physical page size used to bucket L2-source cache-line addresses.",
    )
    parser.add_argument(
        "--read-mostly-pct",
        type=float,
        default=90.0,
        help="Minimum read ratio for shared read-mostly page candidates.",
    )
    parser.add_argument(
        "--out-prefix",
        default="l2_home_evidence",
        help="Output prefix inside result_dir.",
    )
    return parser.parse_args()


def int_value(row, key, default=0):
    value = row.get(key, "")
    if value == "":
        return default
    return int(float(value))


def float_value(row, key, default=0.0):
    value = row.get(key, "")
    if value == "":
        return default
    return float(value)


def pct(numerator, denominator):
    if denominator == 0:
        return 0.0
    return 100.0 * numerator / denominator


def page_base(paddr, page_size):
    return (paddr // page_size) * page_size


def op_prefix_from_page_file(path):
    suffix = "_sharing_pages.csv"
    name = path.name
    if not name.endswith(suffix):
        return path.stem
    return name[: -len(suffix)]


def parse_requester_counts(value):
    counts = {}
    if not value:
        return counts
    for part in value.split(";"):
        if not part:
            continue
        fields = part.split(":")
        if len(fields) < 2:
            continue
        counts[fields[0]] = int(fields[1])
    return counts


def new_source_counter():
    return defaultdict(int)


def read_l2_data_source(path, page_size):
    by_page = defaultdict(new_source_counter)
    if not path.exists():
        return by_page

    with open(path, newline="") as f:
        for row in csv.DictReader(f):
            if row.get("has_paddr") != "true":
                continue
            paddr = int_value(row, "paddr")
            page = page_base(paddr, page_size)
            source = row["source"]
            access_type = row["access_type"]
            key = f"{source}_{access_type}"
            by_page[page][key] += int_value(row, "accesses")
            by_page[page][f"{key}_bytes"] += int_value(row, "bytes")

    return by_page


def read_l2_page_source(path):
    by_page = defaultdict(new_source_counter)
    if not path.exists():
        return by_page

    with open(path, newline="") as f:
        for row in csv.DictReader(f):
            page = int_value(row, "page_paddr")
            source = row["source"]
            access_type = row["access_type"]
            accesses = int_value(row, "accesses")
            bytes_ = int_value(row, "bytes")
            key = f"{source}_{access_type}"
            by_page[page][key] += accesses
            by_page[page][f"{key}_bytes"] += bytes_
            if (
                row.get("is_neighbor") == "true"
                and source.startswith("remote_")
                and access_type == "read"
            ):
                by_page[page]["remote_neighbor_read"] += accesses
                by_page[page]["remote_neighbor_read_bytes"] += bytes_

    return by_page


def read_remote_fill_reuse(path, page_size):
    by_page = defaultdict(new_source_counter)
    if not path.exists():
        return by_page

    with open(path, newline="") as f:
        for row in csv.DictReader(f):
            if row.get("has_paddr") != "true":
                continue
            paddr = int_value(row, "paddr")
            page = page_base(paddr, page_size)
            by_page[page]["remote_dram_fills"] += int_value(
                row, "remote_dram_fills"
            )
            by_page[page]["remote_dram_fill_bytes"] += int_value(
                row, "remote_dram_fill_bytes"
            )
            by_page[page]["local_l2_hit_reuses_after_remote_fill"] += int_value(
                row, "local_l2_hit_reuses"
            )
            by_page[page]["local_l2_hit_reuse_bytes_after_remote_fill"] += int_value(
                row, "local_l2_hit_reuse_bytes"
            )

    return by_page


def classify_page(row, owner_local_accesses, args):
    remote_accesses = int_value(row, "remote_accesses")
    sharers = int_value(row, "sharer_count")
    read_ratio = float_value(row, "read_ratio_pct")

    if remote_accesses == 0:
        return "local_only"
    if sharers >= 2 and read_ratio >= args.read_mostly_pct:
        return "shared_read_mostly_candidate"
    if owner_local_accesses > 0:
        return "owner_and_remote_use"
    if read_ratio < 50.0:
        return "remote_write_or_output_placement"
    return "remote_private_or_placement_mismatch"


def enrich_page_rows(result_dir, page_file, args):
    prefix = op_prefix_from_page_file(page_file)
    data_source = result_dir / f"{prefix}_metrics_l2_source_data_source.csv"
    page_source = result_dir / f"{prefix}_metrics_l2_source_page_source.csv"
    fill_reuse = result_dir / f"{prefix}_metrics_l2_source_remote_fill_reuse.csv"

    if page_source.exists():
        l2_by_page = read_l2_page_source(page_source)
    else:
        l2_by_page = read_l2_data_source(data_source, args.page_size)
    reuse_by_page = read_remote_fill_reuse(fill_reuse, args.page_size)

    enriched = []
    with open(page_file, newline="") as f:
        for row in csv.DictReader(f):
            page_paddr = int_value(row, "page_paddr")
            source = l2_by_page.get(page_paddr, {})
            reuse = reuse_by_page.get(page_paddr, {})
            requester_counts = parse_requester_counts(row.get("requester_counts", ""))
            owner = row.get("dominant_owner", "")
            owner_local_accesses = requester_counts.get(owner, 0)

            remote_l2_read = (
                source.get("remote_l2_cache_read", 0)
                + source.get("remote_l2_mshr_read", 0)
            )
            remote_read_sources = (
                remote_l2_read + source.get("remote_dram_read", 0)
            )

            out = {
                "op": prefix,
                "pid": row.get("pid", ""),
                "page_id": row.get("page_id", ""),
                "page_vaddr": row.get("page_vaddr", ""),
                "page_paddr": row.get("page_paddr", ""),
                "total_accesses": row.get("total_accesses", "0"),
                "read_accesses": row.get("read_accesses", "0"),
                "write_accesses": row.get("write_accesses", "0"),
                "read_ratio_pct": row.get("read_ratio_pct", "0"),
                "remote_accesses": row.get("remote_accesses", "0"),
                "remote_access_ratio_pct": row.get(
                    "remote_access_ratio_pct", "0"
                ),
                "sharer_count": row.get("sharer_count", "0"),
                "remote_sharer_count": row.get("remote_sharer_count", "0"),
                "dominant_owner": owner,
                "owner_local_accesses": owner_local_accesses,
                "owner_local_access_ratio_pct": f"{pct(owner_local_accesses, int_value(row, 'total_accesses')):.6f}",
                "requester_counts": row.get("requester_counts", ""),
            }

            for column in SOURCE_COLUMNS:
                out[column] = source.get(column, 0)

            out["remote_read_l2_service_ratio_pct"] = f"{pct(remote_l2_read, remote_read_sources):.6f}"
            out["remote_read_dram_service_ratio_pct"] = f"{pct(source.get('remote_dram_read', 0), remote_read_sources):.6f}"
            out["remote_neighbor_read_ratio_pct"] = f"{pct(source.get('remote_neighbor_read', 0), remote_read_sources):.6f}"
            out["remote_dram_fills"] = reuse.get("remote_dram_fills", 0)
            out["local_l2_hit_reuses_after_remote_fill"] = reuse.get(
                "local_l2_hit_reuses_after_remote_fill", 0
            )
            out["local_l2_reuse_per_remote_fill"] = f"{reuse.get('local_l2_hit_reuses_after_remote_fill', 0) / reuse.get('remote_dram_fills', 1):.6f}" if reuse.get("remote_dram_fills", 0) else "0.000000"
            out["candidate_class"] = classify_page(row, owner_local_accesses, args)
            enriched.append(out)

    return enriched


def summarize(rows):
    groups = defaultdict(lambda: defaultdict(int))
    for row in rows:
        keys = ["ALL", row["op"], row["candidate_class"]]
        for key in keys:
            group = groups[key]
            group["pages"] += 1
            for col in [
                "total_accesses",
                "read_accesses",
                "write_accesses",
                "remote_accesses",
                "owner_local_accesses",
                "remote_l2_cache_read",
                "remote_l2_mshr_read",
                "remote_dram_read",
                "remote_write_allocate_write",
                "remote_neighbor_read",
                "remote_dram_fills",
                "local_l2_hit_reuses_after_remote_fill",
            ]:
                group[col] += int_value(row, col)
    return groups


def write_csv(path, rows):
    if not rows:
        return
    fieldnames = list(rows[0].keys())
    with open(path, "w", newline="") as f:
        writer = csv.DictWriter(f, fieldnames=fieldnames)
        writer.writeheader()
        writer.writerows(rows)


def write_summary_csv(path, groups):
    fieldnames = [
        "group",
        "pages",
        "total_accesses",
        "remote_accesses",
        "remote_access_ratio_pct",
        "read_accesses",
        "read_ratio_pct",
        "owner_local_accesses",
        "remote_l2_cache_read",
        "remote_l2_mshr_read",
        "remote_dram_read",
        "remote_read_l2_service_ratio_pct",
        "remote_read_dram_service_ratio_pct",
        "remote_neighbor_read_ratio_pct",
        "remote_write_allocate_write",
        "remote_dram_fills",
        "local_l2_hit_reuses_after_remote_fill",
        "local_l2_reuse_per_remote_fill",
    ]
    rows = []
    for key in sorted(groups):
        group = groups[key]
        remote_l2_read = (
            group["remote_l2_cache_read"] + group["remote_l2_mshr_read"]
        )
        remote_read_sources = remote_l2_read + group["remote_dram_read"]
        rows.append(
            {
                "group": key,
                "pages": group["pages"],
                "total_accesses": group["total_accesses"],
                "remote_accesses": group["remote_accesses"],
                "remote_access_ratio_pct": f"{pct(group['remote_accesses'], group['total_accesses']):.6f}",
                "read_accesses": group["read_accesses"],
                "read_ratio_pct": f"{pct(group['read_accesses'], group['total_accesses']):.6f}",
                "owner_local_accesses": group["owner_local_accesses"],
                "remote_l2_cache_read": group["remote_l2_cache_read"],
                "remote_l2_mshr_read": group["remote_l2_mshr_read"],
                "remote_dram_read": group["remote_dram_read"],
                "remote_read_l2_service_ratio_pct": f"{pct(remote_l2_read, remote_read_sources):.6f}",
                "remote_read_dram_service_ratio_pct": f"{pct(group['remote_dram_read'], remote_read_sources):.6f}",
                "remote_neighbor_read_ratio_pct": f"{pct(group['remote_neighbor_read'], remote_read_sources):.6f}",
                "remote_write_allocate_write": group["remote_write_allocate_write"],
                "remote_dram_fills": group["remote_dram_fills"],
                "local_l2_hit_reuses_after_remote_fill": group[
                    "local_l2_hit_reuses_after_remote_fill"
                ],
                "local_l2_reuse_per_remote_fill": f"{group['local_l2_hit_reuses_after_remote_fill'] / group['remote_dram_fills']:.6f}" if group["remote_dram_fills"] else "0.000000",
            }
        )

    with open(path, "w", newline="") as f:
        writer = csv.DictWriter(f, fieldnames=fieldnames)
        writer.writeheader()
        writer.writerows(rows)


def main():
    args = parse_args()
    result_dir = Path(args.result_dir)
    page_files = sorted(result_dir.glob("*_sharing_pages.csv"))
    if not page_files:
        raise SystemExit(f"No *_sharing_pages.csv files found in {result_dir}")

    rows = []
    for page_file in page_files:
        rows.extend(enrich_page_rows(result_dir, page_file, args))

    page_out = result_dir / f"{args.out_prefix}_pages.csv"
    summary_out = result_dir / f"{args.out_prefix}_summary.csv"
    write_csv(page_out, rows)
    write_summary_csv(summary_out, summarize(rows))

    print(f"Wrote {page_out}")
    print(f"Wrote {summary_out}")


if __name__ == "__main__":
    raise SystemExit(main())
