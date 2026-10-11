#!/usr/bin/env python3
"""Stop processes whose executable belongs to one private Codex package directory."""
import os
from pathlib import Path
import signal
import subprocess
import sys
import time


def processes(packages):
    rows = subprocess.check_output(['ps', '-axo', 'pid=,comm='], text=True)
    found = set()
    for row in rows.splitlines():
        fields = row.strip().split(None, 1)
        if len(fields) != 2:
            continue
        pid = int(fields[0])
        # Linux comm is only a name; /proc gives the executable instead.
        if sys.platform.startswith('linux'):
            try:
                executable = os.readlink(f'/proc/{pid}/exe')
            except OSError:
                continue
        else:
            executable = fields[1]
        if not os.path.isabs(executable):
            continue
        executable = Path(executable).resolve()
        if executable.is_relative_to(packages):
            found.add(pid)
    return found


def stop(home):
    home = Path(os.path.abspath(home))
    # macOS exposes these system directories through fixed filesystem aliases.
    for alias in ('/tmp', '/var'):
        if home.is_relative_to(alias):
            home = Path(os.path.realpath(alias)) / home.relative_to(alias)
    if home.resolve() != home:
        raise ValueError('Refusing Codex cleanup through a redirected home path.')
    packages = (home / 'packages').resolve()
    # A package-directory link must not turn cleanup into another installation's kill.
    if not packages.is_relative_to(home) or not packages.is_dir():
        return
    pending = processes(packages)
    for pid in pending:
        if pid not in processes(packages):
            continue
        try:
            os.kill(pid, signal.SIGTERM)
        except ProcessLookupError:
            pass
    deadline = time.monotonic() + 2
    while pending and time.monotonic() < deadline:
        pending &= processes(packages)
        if pending:
            time.sleep(0.05)
    for pid in pending & processes(packages):
        if pid not in processes(packages):
            continue
        try:
            os.kill(pid, signal.SIGKILL)
        except ProcessLookupError:
            pass


if __name__ == '__main__':
    if len(sys.argv) != 2:
        sys.exit('usage: stop-sandbox-codex.py CODEX_HOME')
    stop(sys.argv[1])
