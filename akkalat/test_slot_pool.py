import os
import subprocess
import sys
import tempfile
import threading
import time
import unittest
from unittest import mock

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import runall2  # noqa: E402
import slot_pool  # noqa: E402


class SlotPoolTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.dir = self.tmp.name

    def tearDown(self):
        self.tmp.cleanup()

    def test_count_limits_holders_and_release_frees_a_slot(self):
        pool = slot_pool.SlotPool(self.dir, 2, 0)
        a, b = pool.try_acquire(), pool.try_acquire()
        self.assertIsNotNone(a)
        self.assertIsNotNone(b)
        self.assertIsNone(pool.try_acquire())
        a.close()
        c = pool.try_acquire()
        self.assertIsNotNone(c)
        b.close()
        c.close()

    def test_waiting_higher_priority_blocks_lower_priority(self):
        high = slot_pool.SlotPool(self.dir, 4, 1)
        low = slot_pool.SlotPool(self.dir, 4, 2)
        high.set_waiting(True)
        self.assertIsNone(low.try_acquire())
        slot = high.try_acquire()
        self.assertIsNotNone(slot)
        high.set_waiting(False)
        other = low.try_acquire()
        self.assertIsNotNone(other)
        slot.close()
        other.close()

    def test_stale_waiter_of_dead_process_is_ignored(self):
        dead = subprocess.Popen([sys.executable, "-c", "pass"])
        dead.wait()
        os.makedirs(os.path.join(self.dir, "waiters"), exist_ok=True)
        stale = os.path.join(self.dir, "waiters", f"0001-{dead.pid}")
        open(stale, "w").close()
        slot = slot_pool.SlotPool(self.dir, 1, 5).try_acquire()
        self.assertIsNotNone(slot)
        self.assertFalse(os.path.exists(stale))
        slot.close()

    def test_scheduler_respects_the_shared_slot_count(self):
        running, peak = 0, 0
        lock = threading.Lock()

        def fake_run(exp):
            nonlocal running, peak
            with lock:
                running += 1
                peak = max(peak, running)
            time.sleep(0.05)
            with lock:
                running -= 1
            return {"success": True}

        env = {"EXP_SLOT_DIR": self.dir, "EXP_SLOT_COUNT": "2", "EXP_SLOT_PRIORITY": "3"}
        with mock.patch.dict(os.environ, env), \
                mock.patch.object(slot_pool, "POLL_SECONDS", 0.01), \
                mock.patch.object(runall2, "run_exp", side_effect=fake_run), \
                mock.patch.object(runall2, "terminate_all_processes"):
            runall2.run_all_experiments([{"id": i} for i in range(6)], max_workers=4)
        self.assertEqual(peak, 2)
        self.assertEqual(os.listdir(os.path.join(self.dir, "waiters")), [])

    def test_campaign_at_its_own_limit_does_not_hold_back_others(self):
        # A higher-priority campaign that is at its own worker limit (with
        # runs still pending) must not keep a lower-priority one from using
        # a free shared slot.
        gate = threading.Event()
        started = []

        def fake_run(exp):
            started.append(exp["id"])
            gate.wait(5)
            return {"success": True}

        env = {"EXP_SLOT_DIR": self.dir, "EXP_SLOT_COUNT": "3", "EXP_SLOT_PRIORITY": "1"}
        with mock.patch.dict(os.environ, env), \
                mock.patch.object(slot_pool, "POLL_SECONDS", 0.01), \
                mock.patch.object(runall2, "run_exp", side_effect=fake_run), \
                mock.patch.object(runall2, "terminate_all_processes"):
            high = threading.Thread(target=runall2.run_all_experiments,
                                    args=([{"id": f"h{i}"} for i in range(4)],),
                                    kwargs={"max_workers": 1})
            high.start()
            deadline = time.time() + 5
            while "h0" not in started and time.time() < deadline:
                time.sleep(0.01)
            low = slot_pool.SlotPool(self.dir, 3, 2)
            slot = low.try_acquire()
            self.assertIsNotNone(slot)
            slot.close()
            gate.set()
            high.join(10)

    def test_scheduler_unchanged_without_pool(self):
        peak, running = 0, 0
        lock = threading.Lock()

        def fake_run(exp):
            nonlocal running, peak
            with lock:
                running += 1
                peak = max(peak, running)
            time.sleep(0.05)
            with lock:
                running -= 1
            return {"success": True}

        with mock.patch.dict(os.environ, {}, clear=False), \
                mock.patch.object(runall2, "run_exp", side_effect=fake_run), \
                mock.patch.object(runall2, "terminate_all_processes"):
            os.environ.pop("EXP_SLOT_DIR", None)
            runall2.run_all_experiments([{"id": i} for i in range(6)], max_workers=4)
        self.assertEqual(peak, 4)


if __name__ == "__main__":
    unittest.main()
