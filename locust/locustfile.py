"""Load tests for the Product API.

Two user classes run the exact same task mix, so the only variable between
them is the HTTP client:

    ProductHttpUser      -> locust.HttpUser     (python-requests)
    ProductFastHttpUser  -> locust.FastHttpUser (geventhttpclient)

Pick one on the command line (see docker-compose.yml), e.g.
    locust -f locustfile.py ProductFastHttpUser

Request mix (a read-heavy store, like real e-commerce browsing):
    ~85%  GET  /products/{id}   for a product that exists       -> 200
     ~5%  GET  /products/{id}   for a product that doesn't       -> 404
    ~10%  POST /products/{id}/details with a valid Product       -> 204

Environment variables:
    SEED_PRODUCTS   size of the server's seeded catalog (default 1000)
    WAIT            "none" for zero think time (stress), else 0.1-0.5s (default)
"""

import os
import random

from locust import FastHttpUser, HttpUser, TaskSet, between, constant, task

SEED_PRODUCTS = int(os.getenv("SEED_PRODUCTS", "1000"))
WAIT = constant(0) if os.getenv("WAIT") == "none" else between(0.1, 0.5)


def product_body(product_id):
    return {
        "product_id": product_id,
        "sku": f"SKU-{product_id:06d}-{random.randint(0, 999)}",
        "manufacturer": "Locust Manufacturing",
        "category_id": random.randint(1, 50),
        "weight": random.randint(0, 5000),
        "some_other_id": random.randint(1, 1000),
    }


class ProductTasks(TaskSet):
    """Task set shared by both user classes."""

    @task(17)
    def get_existing_product(self):
        pid = random.randint(1, SEED_PRODUCTS)
        self.client.get(f"/products/{pid}", name="GET /products/:id")

    @task(1)
    def get_missing_product(self):
        pid = random.randint(SEED_PRODUCTS + 1, 2_000_000_000)
        # A 404 is the expected answer here, so don't count it as a failure.
        with self.client.get(
            f"/products/{pid}", name="GET /products/:id (missing)", catch_response=True
        ) as resp:
            if resp.status_code == 404:
                resp.success()
            else:
                resp.failure(f"expected 404, got {resp.status_code}")

    @task(2)
    def post_product_details(self):
        pid = random.randint(1, SEED_PRODUCTS)
        self.client.post(
            f"/products/{pid}/details",
            json=product_body(pid),
            name="POST /products/:id/details",
        )


class ProductHttpUser(HttpUser):
    tasks = [ProductTasks]
    wait_time = WAIT


class ProductFastHttpUser(FastHttpUser):
    tasks = [ProductTasks]
    wait_time = WAIT
