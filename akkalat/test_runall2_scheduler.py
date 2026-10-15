"""Launch pacing, stopped-child hold, and stall watchdog of runall2."""

import contextlib
import io
import os
import re
import sys
import tempfile
import threading
import time
import unittest
from unittest import mock

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import run_cupath_llm  # noqa: E402
import run_cupath_sensitivity  # noqa: E402
import runall2  # noqa: E402
import runall2_args  # noqa: E402


@contextlib.contextmanager
def scheduler_without_pool(fake_run):
    """Runs the scheduler with fake experiments and no shared slot pool."""
    with mock.patch.dict(os.environ), \
            mock.patch.object(runall2, "run_exp", side_effect=fake_run), \
            mock.patch.object(runall2, "terminate_all_processes"):
        os.environ.pop("EXP_SLOT_DIR", None)
        yield


class ProcessStateTest(unittest.TestCase):
    def test_state_follows_the_last_parenthesis_of_the_command(self):
        with tempfile.TemporaryDirectory() as root:
            os.makedirs(os.path.join(root, "42"))
            with open(os.path.join(root, "42", "stat"), "w") as stream:
                stream.write("42 (base line) (x)) T 1 42 42 0 -1\n")
            self.assertEqual(runall2.read_process_state(42, root), "T")
            self.assertIsNone(runall2.read_process_state(43, root))

    def test_unreadable_state_counts_as_running(self):
        states = {1: "R", 2: "T", 3: None, 4: "S"}
        self.assertEqual(
            runall2.count_child_states([1, 2, 3, 4], states.get), (3, 1)
        )


class LaunchPacingTest(unittest.TestCase):
    def test_one_launch_per_interval_each_after_a_memory_read(self):
        events = []
        launches = []
        lock = threading.Lock()

        def mem_available():
            with lock:
                events.append("mem")
            return 100.0

        def fake_run(exp):
            with lock:
                events.append("launch")
                launches.append(time.monotonic())
            time.sleep(0.02)
            return {"success": True}

        with scheduler_without_pool(fake_run), \
                contextlib.redirect_stdout(io.StringIO()) as out:
            runall2.run_all_experiments(
                [{"id": i} for i in range(4)],
                max_workers=4,
                initial_workers=4,
                memory_reserve_gib=50,
                memory_scan_seconds=1000,
                mem_available_fn=mem_available,
                launch_interval_seconds=0.05,
            )
        self.assertEqual(events, ["mem", "launch"] * 4)
        gaps = [b - a for a, b in zip(launches, launches[1:])]
        self.assertTrue(all(gap >= 0.045 for gap in gaps), gaps)
        self.assertEqual(out.getvalue().count("Paced launch"), 4)

    def test_without_interval_every_admitted_slot_fills_at_once(self):
        started = []
        gate = threading.Event()

        def fake_run(exp):
            started.append(exp["id"])
            gate.wait(5)
            return {"success": True}

        with scheduler_without_pool(fake_run), \
                contextlib.redirect_stdout(io.StringIO()) as out:
            worker = threading.Thread(
                target=runall2.run_all_experiments,
                args=([{"id": i} for i in range(4)], 4),
            )
            worker.start()
            deadline = time.monotonic() + 5
            while len(started) < 4 and time.monotonic() < deadline:
                time.sleep(0.01)
            gate.set()
            worker.join(10)
        self.assertEqual(sorted(started), [0, 1, 2, 3])
        self.assertNotIn("Paced launch", out.getvalue())

    def test_negative_interval_is_rejected(self):
        with self.assertRaises(ValueError):
            runall2.run_all_experiments(
                [], 1, launch_interval_seconds=-1)


class StoppedChildHoldTest(unittest.TestCase):
    def test_no_launch_while_a_child_is_stopped(self):
        started = []
        gate = threading.Event()
        resumed = threading.Event()
        calls = 0

        def child_states():
            # Nothing runs before the first launch; afterwards its child is
            # stopped until the test resumes it.
            nonlocal calls
            calls += 1
            if calls == 1:
                return 0, 0
            return (1, 0) if resumed.is_set() else (0, 1)

        def fake_run(exp):
            started.append(exp["id"])
            if exp["id"] == 0:
                gate.wait(5)
            return {"success": True}

        with scheduler_without_pool(fake_run), \
                mock.patch.object(runall2, "STOPPED_POLL_SECONDS", 0.01), \
                contextlib.redirect_stdout(io.StringIO()) as out:
            worker = threading.Thread(
                target=runall2.run_all_experiments,
                args=([{"id": i} for i in range(2)], 2),
                kwargs={"child_states_fn": child_states},
            )
            worker.start()
            time.sleep(0.15)
            held = list(started)
            resumed.set()
            deadline = time.monotonic() + 5
            while len(started) < 2 and time.monotonic() < deadline:
                time.sleep(0.01)
            gate.set()
            worker.join(10)
        self.assertEqual(held, [0])
        self.assertEqual(started, [0, 1])
        log = out.getvalue()
        self.assertEqual(log.count("Holding launches"), 1)
        self.assertIn("running=0, stopped=1", log)
        self.assertIn("launches resume", log)

    def test_memory_scan_logs_children_and_does_not_admit_while_stopped(self):
        gate = threading.Event()
        calls = 0

        def child_states():
            nonlocal calls
            calls += 1
            return (0, 0) if calls == 1 or gate.is_set() else (1, 1)

        def fake_run(exp):
            gate.wait(5)
            return {"success": True}

        with scheduler_without_pool(fake_run), \
                mock.patch.object(runall2, "STOPPED_POLL_SECONDS", 0.01), \
                contextlib.redirect_stdout(io.StringIO()) as out:
            worker = threading.Thread(
                target=runall2.run_all_experiments,
                args=([{"id": i} for i in range(2)], 2),
                kwargs={
                    "initial_workers": 1,
                    "memory_reserve_gib": 50,
                    "memory_scan_seconds": 0.02,
                    "mem_available_fn": lambda: 100.0,
                    "child_states_fn": child_states,
                },
            )
            worker.start()
            time.sleep(0.15)
            log = out.getvalue()
            gate.set()
            worker.join(10)
        self.assertIn(
            "Memory scan kept current concurrency: MemAvailable=100.0 GiB, "
            "reserve=50.0 GiB, concurrency=1/2, "
            "children running=1, stopped=1",
            log,
        )
        self.assertNotIn("admitted one additional", log)


class StallWatchdogTest(unittest.TestCase):
    def test_no_stdout_growth_alerts_once_per_interval(self):
        watchdog = runall2.StallWatchdog(60, now=0)
        self.assertIsNone(watchdog.check(0, {"a": 10}, 1, 0))
        self.assertIsNone(watchdog.check(30, {"a": 20}, 1, 0))
        self.assertIsNone(watchdog.check(80, {"a": 20}, 1, 0))
        alert = watchdog.check(90, {"a": 20}, 1, 0)
        self.assertIn("STALL", alert)
        self.assertIn("no simulator stdout grew for 1.0 minutes", alert)
        self.assertIsNone(watchdog.check(120, {"a": 20}, 1, 0))
        self.assertIsNotNone(watchdog.check(150, {"a": 20}, 1, 0))
        # Output resumes: no alert until another full quiet interval.
        self.assertIsNone(watchdog.check(160, {"a": 25}, 1, 0))
        self.assertIsNone(watchdog.check(210, {"a": 25}, 1, 0))
        self.assertIsNotNone(watchdog.check(220, {"a": 25}, 1, 0))

    def test_all_children_stopped_is_named(self):
        watchdog = runall2.StallWatchdog(60, now=0)
        self.assertIsNone(watchdog.check(10, {"a": 5, "b": 7}, 0, 2))
        alert = watchdog.check(70, {"a": 5, "b": 7}, 0, 2)
        self.assertIn("all 2 simulator children stopped (state T)", alert)
        self.assertIn("running=0, stopped=2", alert)

    def test_finished_child_and_new_child_count_as_progress(self):
        watchdog = runall2.StallWatchdog(60, now=0)
        watchdog.check(0, {"a": 5}, 1, 0)
        watchdog.progress(50)
        self.assertIsNone(watchdog.check(100, {"a": 5}, 1, 0))
        self.assertIsNone(watchdog.check(140, {"a": 5, "b": 0}, 2, 0))
        self.assertIsNotNone(watchdog.check(200, {"a": 5, "b": 0}, 2, 0))

    def test_campaign_without_children_is_not_stalled(self):
        watchdog = runall2.StallWatchdog(60, now=0)
        self.assertIsNone(watchdog.check(500, {}, 0, 0))
        self.assertIsNone(watchdog.check(550, {"a": 0}, 1, 0))

    def test_scheduler_prints_stall_lines_when_children_are_silent(self):
        def fake_run(exp):
            time.sleep(0.3)
            return {"success": True}

        with scheduler_without_pool(fake_run), \
                mock.patch.object(runall2, "WATCHDOG_POLL_SECONDS", 0.01), \
                mock.patch.object(runall2, "child_stdout_sizes",
                                  return_value={"out": 10}), \
                contextlib.redirect_stdout(io.StringIO()) as out:
            runall2.run_all_experiments(
                [{"id": 0}],
                1,
                stall_alert_seconds=0.1,
                child_states_fn=lambda: (1, 0),
            )
        stalls = [line for line in out.getvalue().splitlines()
                  if line.startswith("*** STALL")]
        self.assertTrue(1 <= len(stalls) <= 4, stalls)


class OptionWiringTest(unittest.TestCase):
    def test_runall2_options_default_off(self):
        with mock.patch.object(sys, "argv", ["runall2.py"]):
            args = runall2_args.parse_args()
        self.assertEqual(args.launch_interval_seconds, 0.0)
        self.assertEqual(args.stall_alert_minutes, 0.0)
        with mock.patch.object(sys, "argv", [
            "runall2.py", "--launch-interval-seconds=180",
            "--stall-alert-minutes=30",
        ]):
            args = runall2_args.parse_args()
        self.assertEqual(args.launch_interval_seconds, 180.0)
        self.assertEqual(args.stall_alert_minutes, 30.0)

    def test_llm_options_are_parsed_and_validated(self):
        args = run_cupath_llm.parse_args([
            "--launch-interval-seconds=180", "--stall-alert-minutes=30",
        ])
        self.assertEqual(args.launch_interval_seconds, 180.0)
        self.assertEqual(args.stall_alert_minutes, 30.0)
        run_cupath_llm.validate_args(args)
        args.launch_interval_seconds = -1
        with self.assertRaises(ValueError):
            run_cupath_llm.validate_args(args)

    def test_sensitivity_options_are_parsed_and_validated(self):
        with tempfile.TemporaryDirectory() as directory, \
                mock.patch.object(sys, "argv", [
                    "run_cupath_sensitivity.py", f"--output-dir={directory}",
                    "--launch-interval-seconds=180",
                    "--stall-alert-minutes=30",
                ]):
            args = run_cupath_sensitivity.parse_args()
        self.assertEqual(args.launch_interval_seconds, 180.0)
        self.assertEqual(args.stall_alert_minutes, 30.0)
        with mock.patch.object(sys, "argv", [
            "run_cupath_sensitivity.py", "--stall-alert-minutes=-1",
        ]), contextlib.redirect_stderr(io.StringIO()):
            with self.assertRaises(SystemExit):
                run_cupath_sensitivity.parse_args()

    def test_launchers_pass_the_options_to_the_scheduler(self):
        here = os.path.dirname(os.path.abspath(__file__))
        for name in ("runall2.py", "run_cupath_llm.py",
                     "run_cupath_sensitivity.py"):
            with open(os.path.join(here, name), encoding="utf-8") as stream:
                source = stream.read()
            calls = len(re.findall(
                r"^\s+run_all_experiments\($", source, re.MULTILINE))
            self.assertGreater(calls, 0, name)
            self.assertEqual(
                source.count("launch_interval_seconds=args."), calls, name)
            self.assertEqual(
                source.count("stall_alert_seconds=args."), calls, name)


if __name__ == "__main__":
    unittest.main()
