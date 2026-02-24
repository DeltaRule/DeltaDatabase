---
layout: default
title: E-Commerce Catalogue
parent: Examples
grand_parent: Usage
nav_order: 5
---

# Example: E-Commerce Catalogue

Store and manage a product catalogue, inventory, and order records. DeltaDatabase's JSON schema validation ensures product data integrity across your catalogue.

**Use case:** Product catalogue, inventory management, order storage, shopping carts.

**Databases:**
- `products` — product catalogue (keyed by SKU)
- `inventory` — stock levels (keyed by SKU)
- `orders` — order records (keyed by order ID)
- `carts` — active shopping carts (keyed by session/user ID)

---

## Schema Setup

```bash
TOKEN=$(curl -s -X POST http://127.0.0.1:8080/api/login \
  -H 'Content-Type: application/json' \
  -d '{"client_id":"admin"}' | jq -r .token)

# Product schema
curl -s -X PUT http://127.0.0.1:8080/schema/product.v1 \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{
    "$schema": "http://json-schema.org/draft-07/schema#",
    "$id": "product.v1",
    "type": "object",
    "properties": {
      "sku":         {"type": "string"},
      "name":        {"type": "string"},
      "description": {"type": "string"},
      "price":       {"type": "number", "minimum": 0},
      "currency":    {"type": "string", "enum": ["USD", "EUR", "GBP"]},
      "category":    {"type": "string"},
      "tags":        {"type": "array", "items": {"type": "string"}},
      "images":      {"type": "array", "items": {"type": "string"}},
      "attributes":  {"type": "object"}
    },
    "required": ["sku", "name", "price", "currency"]
  }'

# Order schema
curl -s -X PUT http://127.0.0.1:8080/schema/order.v1 \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{
    "$schema": "http://json-schema.org/draft-07/schema#",
    "$id": "order.v1",
    "type": "object",
    "properties": {
      "order_id":   {"type": "string"},
      "user_id":    {"type": "string"},
      "status":     {"type": "string", "enum": ["pending","confirmed","shipped","delivered","cancelled"]},
      "items": {
        "type": "array",
        "items": {
          "type": "object",
          "properties": {
            "sku":      {"type": "string"},
            "name":     {"type": "string"},
            "quantity": {"type": "integer", "minimum": 1},
            "price":    {"type": "number", "minimum": 0}
          },
          "required": ["sku", "quantity", "price"]
        }
      },
      "total":      {"type": "number", "minimum": 0},
      "currency":   {"type": "string"},
      "created_at": {"type": "string", "format": "date-time"}
    },
    "required": ["order_id", "user_id", "status", "items", "total", "currency"]
  }'
```

---

## Python Client

```python
"""
ecommerce.py — E-Commerce catalogue with DeltaDatabase

Install: pip install requests
"""
import requests
import uuid
from datetime import datetime, timezone

BASE_URL = "http://127.0.0.1:8080"


class ECommerceClient:
    def __init__(self, client_id: str = "shop-service"):
        self.session = requests.Session()
        resp = self.session.post(
            f"{BASE_URL}/api/login", json={"client_id": client_id}
        )
        resp.raise_for_status()
        self.session.headers["Authorization"] = f"Bearer {resp.json()['token']}"

    # ── Products ──────────────────────────────────────────────────────────

    def add_product(self, sku: str, name: str, price: float,
                    currency: str = "USD", **kwargs) -> dict:
        """Add or update a product in the catalogue."""
        product = {"sku": sku, "name": name, "price": price,
                   "currency": currency, **kwargs}
        self.session.put(f"{BASE_URL}/entity/products",
                         json={sku: product}).raise_for_status()
        print(f"Added product: {sku} — {name} ({currency} {price:.2f})")
        return product

    def get_product(self, sku: str) -> dict | None:
        resp = self.session.get(f"{BASE_URL}/entity/products",
                                params={"key": sku})
        if resp.status_code == 404:
            return None
        resp.raise_for_status()
        return resp.json()

    def update_price(self, sku: str, new_price: float) -> None:
        product = self.get_product(sku)
        if product is None:
            raise ValueError(f"Product '{sku}' not found")
        product["price"] = new_price
        self.session.put(f"{BASE_URL}/entity/products",
                         json={sku: product}).raise_for_status()
        print(f"Updated price for {sku}: {new_price:.2f} {product['currency']}")

    # ── Inventory ─────────────────────────────────────────────────────────

    def set_stock(self, sku: str, quantity: int, warehouse: str = "main") -> None:
        """Set stock level for a SKU."""
        inventory = {"sku": sku, "warehouse": warehouse,
                     "quantity": quantity,
                     "updated_at": datetime.now(timezone.utc).isoformat()}
        self.session.put(f"{BASE_URL}/entity/inventory",
                         json={f"{sku}.{warehouse}": inventory}).raise_for_status()
        print(f"Stock set: {sku} @ {warehouse} = {quantity} units")

    def get_stock(self, sku: str, warehouse: str = "main") -> int:
        resp = self.session.get(f"{BASE_URL}/entity/inventory",
                                params={"key": f"{sku}.{warehouse}"})
        if resp.status_code == 404:
            return 0
        resp.raise_for_status()
        return resp.json().get("quantity", 0)

    def reserve_stock(self, sku: str, quantity: int,
                      warehouse: str = "main") -> bool:
        """Reserve stock for an order. Returns False if insufficient stock."""
        key = f"{sku}.{warehouse}"
        resp = self.session.get(f"{BASE_URL}/entity/inventory",
                                params={"key": key})
        if resp.status_code == 404:
            return False
        inventory = resp.json()
        if inventory["quantity"] < quantity:
            return False
        inventory["quantity"] -= quantity
        inventory["updated_at"] = datetime.now(timezone.utc).isoformat()
        self.session.put(f"{BASE_URL}/entity/inventory",
                         json={key: inventory}).raise_for_status()
        return True

    # ── Orders ────────────────────────────────────────────────────────────

    def create_order(self, user_id: str, items: list[dict]) -> dict:
        """
        Create a new order.
        items: [{"sku": "...", "quantity": N, "price": X.XX}, ...]
        """
        order_id = f"order_{uuid.uuid4().hex[:12]}"
        total = sum(i["price"] * i["quantity"] for i in items)
        order = {
            "order_id":   order_id,
            "user_id":    user_id,
            "status":     "pending",
            "items":      items,
            "total":      round(total, 2),
            "currency":   "USD",
            "created_at": datetime.now(timezone.utc).isoformat(),
        }
        self.session.put(f"{BASE_URL}/entity/orders",
                         json={order_id: order}).raise_for_status()
        print(f"Order created: {order_id} (total: USD {total:.2f})")
        return order

    def update_order_status(self, order_id: str, status: str) -> None:
        resp = self.session.get(f"{BASE_URL}/entity/orders",
                                params={"key": order_id})
        resp.raise_for_status()
        order = resp.json()
        old_status = order["status"]
        order["status"] = status
        self.session.put(f"{BASE_URL}/entity/orders",
                         json={order_id: order}).raise_for_status()
        print(f"Order {order_id}: {old_status} → {status}")


if __name__ == "__main__":
    client = ECommerceClient()

    # ── 1. Load the catalogue ──────────────────────────────────────
    print("=== Loading Catalogue ===")
    client.add_product("LAPTOP-PRO-15", "Pro Laptop 15\"", 1299.99,
                       category="Electronics",
                       tags=["laptop", "professional"],
                       attributes={"ram_gb": 16, "storage_gb": 512, "screen_inches": 15.6})

    client.add_product("WIRELESS-MOUSE", "Ergonomic Wireless Mouse", 49.99,
                       category="Accessories",
                       tags=["mouse", "wireless", "ergonomic"])

    client.add_product("USB-C-HUB", "7-in-1 USB-C Hub", 39.99,
                       category="Accessories",
                       tags=["hub", "usb-c"])

    # ── 2. Set initial stock ───────────────────────────────────────
    print("\n=== Setting Stock ===")
    client.set_stock("LAPTOP-PRO-15",  50)
    client.set_stock("WIRELESS-MOUSE", 200)
    client.set_stock("USB-C-HUB",      150)

    # ── 3. Customer places an order ───────────────────────────────
    print("\n=== Processing Order ===")
    order_items = [
        {"sku": "LAPTOP-PRO-15",  "name": "Pro Laptop 15\"",        "quantity": 1, "price": 1299.99},
        {"sku": "WIRELESS-MOUSE", "name": "Ergonomic Wireless Mouse","quantity": 2, "price":   49.99},
    ]

    # Reserve stock
    for item in order_items:
        if client.reserve_stock(item["sku"], item["quantity"]):
            print(f"  Reserved {item['quantity']}x {item['sku']}")
        else:
            print(f"  ❌ Insufficient stock for {item['sku']}")

    order = client.create_order("user_001", order_items)

    # ── 4. Update order status ─────────────────────────────────────
    print("\n=== Fulfilling Order ===")
    client.update_order_status(order["order_id"], "confirmed")
    client.update_order_status(order["order_id"], "shipped")

    # ── 5. Verify stock was updated ────────────────────────────────
    print("\n=== Current Stock ===")
    for sku in ["LAPTOP-PRO-15", "WIRELESS-MOUSE", "USB-C-HUB"]:
        stock = client.get_stock(sku)
        print(f"  {sku}: {stock} units")
```

---

## curl Walkthrough

```bash
BASE="http://127.0.0.1:8080"
TOKEN=$(curl -sf -X POST "$BASE/api/login" \
  -H 'Content-Type: application/json' \
  -d '{"client_id":"shop"}' | jq -r .token)

# Add a product
curl -sf -X PUT "$BASE/entity/products" \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{
    "HDMI-CABLE-2M": {
      "sku":      "HDMI-CABLE-2M",
      "name":     "HDMI 2.1 Cable 2m",
      "price":    14.99,
      "currency": "USD",
      "category": "Cables",
      "tags":     ["hdmi", "cable", "4k"]
    }
  }'

# Look up the product
curl -sf "$BASE/entity/products?key=HDMI-CABLE-2M" \
  -H "Authorization: Bearer $TOKEN" | jq .

# Apply a 10% discount
PRODUCT=$(curl -sf "$BASE/entity/products?key=HDMI-CABLE-2M" \
  -H "Authorization: Bearer $TOKEN")
DISCOUNTED=$(echo "$PRODUCT" | jq '.price = (.price * 0.9 | round * 100 / 100)')
curl -sf -X PUT "$BASE/entity/products" \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d "{\"HDMI-CABLE-2M\": $DISCOUNTED}"
echo "Discount applied: $(echo $DISCOUNTED | jq .price)"
```

---

## Shopping Cart Pattern

```python
class CartClient:
    """Manage shopping carts per user session."""

    def __init__(self, session_token: str):
        self.session = requests.Session()
        self.session.headers["Authorization"] = f"Bearer {session_token}"

    def get_cart(self, user_id: str) -> dict:
        resp = self.session.get(f"{BASE_URL}/entity/carts", params={"key": user_id})
        if resp.status_code == 404:
            return {"user_id": user_id, "items": [], "updated_at": None}
        resp.raise_for_status()
        return resp.json()

    def add_to_cart(self, user_id: str, sku: str, quantity: int, price: float) -> None:
        cart = self.get_cart(user_id)
        # Update quantity if SKU already in cart
        for item in cart["items"]:
            if item["sku"] == sku:
                item["quantity"] += quantity
                break
        else:
            cart["items"].append({"sku": sku, "quantity": quantity, "price": price})
        cart["updated_at"] = datetime.now(timezone.utc).isoformat()
        self.session.put(f"{BASE_URL}/entity/carts",
                         json={user_id: cart}).raise_for_status()

    def clear_cart(self, user_id: str) -> None:
        cart = self.get_cart(user_id)
        cart["items"] = []
        self.session.put(f"{BASE_URL}/entity/carts",
                         json={user_id: cart}).raise_for_status()

    def get_total(self, user_id: str) -> float:
        cart = self.get_cart(user_id)
        return sum(i["price"] * i["quantity"] for i in cart["items"])
```
