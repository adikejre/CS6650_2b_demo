"""Step-ramp stress test: keep adding users until the server breaks.

Each step adds STEP_USERS users and holds for STEP_SECONDS, up to MAX_USERS.
Watching where p95 latency bends upward (and failures start) shows the
server's saturation point far more clearly than one fixed user count.

    locust -f stress.py ProductFastHttpUser          # pick the client class
    STEP_USERS=50 STEP_SECONDS=60 MAX_USERS=1000 ...  # tune the ramp

Run with WAIT=none so every user fires back-to-back requests.
"""

import os

from locust import LoadTestShape

from locustfile import ProductFastHttpUser, ProductHttpUser  # noqa: F401

STEP_USERS = int(os.getenv("STEP_USERS", "50"))
STEP_SECONDS = int(os.getenv("STEP_SECONDS", "60"))
MAX_USERS = int(os.getenv("MAX_USERS", "1000"))


class StepRamp(LoadTestShape):
    def tick(self):
        run_time = self.get_run_time()
        step = int(run_time // STEP_SECONDS) + 1
        users = step * STEP_USERS
        if users > MAX_USERS:
            return None  # stop the test
        return users, STEP_USERS  # (user count, spawn rate per second)
