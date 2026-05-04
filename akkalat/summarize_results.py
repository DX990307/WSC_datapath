import argparse
import csv
import re
from pathlib import Path


METRIC_FILE_PATTERN = re.compile(
    r"^(?P<target>[^_]+)_(?P<benchmark>[^_]+)_(?P<hyper>.+)_metrics\.csv$"
)


def parse_args():
    parser = argparse.ArgumentParser(
        description=(
            "Summarize metric CSV files into a benchmark-by-hyperparameter table "
            "using Driver/total_time values."
        )
    )
    parser.add_argument(
        "results_dir",
        help="Directory containing *_metrics.csv files.",
    )
    parser.add_argument(
        "--output",
        default="summary.csv",
        help=(
            "Output CSV file path. If relative, it is created under results_dir. "
            "Default: summary.csv"
        ),
    )
    parser.add_argument(
        "--where",
        default="Driver",
        help='Metric "where" field to match. Default: Driver',
    )
    parser.add_argument(
        "--what",
        default="total_time",
        help='Metric "what" field to match. Default: total_time',
    )
    return parser.parse_args()


def extract_metric_value(csv_path: Path, where_key: str, what_key: str):
    with csv_path.open("r", newline="") as f:
        reader = csv.reader(f, skipinitialspace=True)
        for row in reader:
            if len(row) < 4:
                continue

            where = row[1].strip()
            what = row[2].strip()
            if where == where_key and what == what_key:
                # Keep value as raw string to avoid changing number format.
                return row[3].strip()

    return None


def discover_data(results_dir: Path, where_key: str, what_key: str):
    table = {}
    unmatched_files = []
    missing_metric_files = []

    for csv_path in sorted(results_dir.glob("*_metrics.csv")):
        match = METRIC_FILE_PATTERN.match(csv_path.name)
        if not match:
            unmatched_files.append(csv_path.name)
            continue

        benchmark = match.group("benchmark")
        hyper = match.group("hyper")

        value = extract_metric_value(csv_path, where_key, what_key)
        if value is None:
            missing_metric_files.append(csv_path.name)
            continue

        benchmark_row = table.setdefault(benchmark, {})
        benchmark_row[hyper] = value

    return table, unmatched_files, missing_metric_files


def write_summary_csv(output_path: Path, table):
    benchmarks = sorted(table.keys())
    hyperparams = sorted(
        {hyper for benchmark in benchmarks for hyper in table[benchmark].keys()}
    )

    with output_path.open("w", newline="") as f:
        writer = csv.writer(f)
        writer.writerow(["benchmark", *hyperparams])

        for benchmark in benchmarks:
            row = [benchmark]
            for hyper in hyperparams:
                row.append(table[benchmark].get(hyper, ""))
            writer.writerow(row)

    return benchmarks, hyperparams


def main():
    args = parse_args()
    results_dir = Path(args.results_dir).resolve()
    if not results_dir.is_dir():
        raise ValueError(f"results directory does not exist: {results_dir}")

    output_path = Path(args.output)
    if not output_path.is_absolute():
        output_path = results_dir / output_path

    table, unmatched_files, missing_metric_files = discover_data(
        results_dir, args.where, args.what
    )
    if not table:
        raise RuntimeError(
            f"No usable metric files found in {results_dir} for "
            f"where={args.where}, what={args.what}"
        )

    benchmarks, hyperparams = write_summary_csv(output_path, table)

    print(f"Wrote summary: {output_path}")
    print(f"Benchmarks: {len(benchmarks)}")
    print(f"Hyper-parameters: {len(hyperparams)}")
    print(f"Matched files: {sum(len(v) for v in table.values())}")

    if unmatched_files:
        print(f"Skipped unmatched filename format: {len(unmatched_files)}")
    if missing_metric_files:
        print(
            f"Skipped files missing metric {args.where}/{args.what}: "
            f"{len(missing_metric_files)}"
        )


if __name__ == "__main__":
    main()
