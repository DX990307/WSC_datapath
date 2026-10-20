"""Cross-process slot pool: cap the simulations of several campaigns at once.

Each run_all_experiments() call schedules only its own campaign. When several
campaigns share a server, setting

    EXP_SLOT_DIR=<dir>  EXP_SLOT_COUNT=16  EXP_SLOT_PRIORITY=<n>

makes every launch take one of EXP_SLOT_COUNT file locks in <dir> first, so
the campaigns together never run more than EXP_SLOT_COUNT simulations, and a
freed slot goes to the waiting campaign with the lowest priority number.
Locks are flock()s, so a slot is released even if its holder dies.

    python3 akkalat/slot_pool.py run -- <command ...>
        Waits for a slot (with the environment's priority), runs the command
        while holding it, and exits with the command's exit code.
"""

import fcntl
import os
import subprocess
import sys
import time

POLL_SECONDS = 15.0


class SlotPool:
    def __init__(self, directory, count, priority):
        self.directory = directory
        self.count = count
        self.priority = priority
        self.waiter = os.path.join(directory, "waiters", f"{priority:04d}-{os.getpid()}")
        os.makedirs(os.path.join(directory, "waiters"), exist_ok=True)

    @classmethod
    def from_env(cls):
        directory = os.environ.get("EXP_SLOT_DIR")
        if not directory:
            return None
        return cls(directory, int(os.environ.get("EXP_SLOT_COUNT", "16")),
                   int(os.environ.get("EXP_SLOT_PRIORITY", "0")))

    def set_waiting(self, waiting):
        """Registers (or removes) this process as having work to launch."""
        if waiting:
            open(self.waiter, "w").close()
        else:
            try:
                os.remove(self.waiter)
            except FileNotFoundError:
                pass

    def _blocked_by_higher_priority(self):
        for name in os.listdir(os.path.join(self.directory, "waiters")):
            try:
                priority, pid = (int(x) for x in name.split("-"))
            except ValueError:
                continue
            if priority >= self.priority:
                continue
            try:
                os.kill(pid, 0)
            except ProcessLookupError:  # stale entry of a dead process
                try:
                    os.remove(os.path.join(self.directory, "waiters", name))
                except FileNotFoundError:
                    pass
                continue
            except PermissionError:
                pass
            return True
        return False

    def try_acquire(self):
        """Returns an open, locked slot file, or None. Close it to release."""
        if self._blocked_by_higher_priority():
            return None
        for i in range(self.count):
            f = open(os.path.join(self.directory, f"slot-{i:02d}"), "a")
            try:
                fcntl.flock(f, fcntl.LOCK_EX | fcntl.LOCK_NB)
                return f
            except BlockingIOError:
                f.close()
        return None

    def acquire(self):
        self.set_waiting(True)
        try:
            while True:
                slot = self.try_acquire()
                if slot is not None:
                    return slot
                time.sleep(POLL_SECONDS)
        finally:
            self.set_waiting(False)


def main():
    if len(sys.argv) < 4 or sys.argv[1] != "run" or sys.argv[2] != "--":
        sys.exit(__doc__)
    pool = SlotPool.from_env()
    slot = pool.acquire() if pool else None
    try:
        return subprocess.call(sys.argv[3:])
    finally:
        if slot is not None:
            slot.close()


if __name__ == "__main__":
    sys.exit(main())
