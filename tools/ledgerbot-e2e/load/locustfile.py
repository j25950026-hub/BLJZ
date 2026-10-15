from __future__ import annotations

import os

from locust import HttpUser, between, task


class LedgerBotHttpUser(HttpUser):
    wait_time = between(1, 5)

    @property
    def admin_token(self) -> str:
        return os.getenv("ADMIN_TOKEN", "")

    @task
    def admin_groups(self):
        if self.admin_token:
            self.client.get(f"/admin/groups?token={self.admin_token}", name="/admin/groups")

    @task
    def admin_orders(self):
        if self.admin_token:
            self.client.get(f"/admin/orders?token={self.admin_token}", name="/admin/orders")

    @task
    def tg_webapp(self):
        self.client.get("/tg/webapp", name="/tg/webapp")

