---
layout: default
title: IoT Sensor Data
parent: Examples
grand_parent: Usage
nav_order: 3
---

# Example: IoT Sensor Data

Store and retrieve sensor readings from IoT devices. Each device has its own entity containing its latest readings and a rolling history.

**Use case:** Smart home, industrial monitoring, environmental sensors, fleet telemetry.

**Database:** `iotdb`  
**Entity key:** device ID (e.g., `sensor_living_room`, `device_A3F2`)  
**Entity value:** `{"device_id": "...", "latest": {...}, "history": [...]}`

---

## Schema Setup

```bash
TOKEN=$(curl -s -X POST http://127.0.0.1:8080/api/login \
  -H 'Content-Type: application/json' \
  -d '{"client_id":"iot-service"}' | jq -r .token)

curl -s -X PUT http://127.0.0.1:8080/schema/iot_device.v1 \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{
    "$schema": "http://json-schema.org/draft-07/schema#",
    "$id": "iot_device.v1",
    "type": "object",
    "properties": {
      "device_id": {"type": "string"},
      "name":      {"type": "string"},
      "location":  {"type": "string"},
      "latest": {
        "type": "object",
        "properties": {
          "timestamp":   {"type": "string", "format": "date-time"},
          "temperature": {"type": "number"},
          "humidity":    {"type": "number", "minimum": 0, "maximum": 100},
          "pressure":    {"type": "number"},
          "battery_pct": {"type": "number", "minimum": 0, "maximum": 100}
        }
      },
      "history": {
        "type": "array",
        "maxItems": 100,
        "items": {
          "type": "object",
          "properties": {
            "timestamp":   {"type": "string"},
            "temperature": {"type": "number"},
            "humidity":    {"type": "number"}
          }
        }
      }
    },
    "required": ["device_id"]
  }'
```

---

## Python Client

```python
"""
iot_client.py — IoT sensor data storage with DeltaDatabase

Install: pip install requests
"""
import requests
from datetime import datetime, timezone
from typing import Optional

BASE_URL = "http://127.0.0.1:8080"
DATABASE = "iotdb"
MAX_HISTORY = 100  # keep last 100 readings per device


class IoTClient:
    def __init__(self, client_id: str = "iot-service"):
        self.session = requests.Session()
        resp = self.session.post(
            f"{BASE_URL}/api/login", json={"client_id": client_id}
        )
        resp.raise_for_status()
        self.session.headers["Authorization"] = f"Bearer {resp.json()['token']}"

    def register_device(self, device_id: str, name: str, location: str) -> None:
        """Register a new IoT device."""
        device = {
            "device_id": device_id,
            "name":      name,
            "location":  location,
            "latest":    {},
            "history":   [],
        }
        self.session.put(
            f"{BASE_URL}/entity/{DATABASE}", json={device_id: device}
        ).raise_for_status()
        print(f"Registered device '{device_id}' at '{location}'")

    def push_reading(
        self,
        device_id: str,
        temperature: float,
        humidity: float,
        pressure: Optional[float] = None,
        battery_pct: Optional[float] = None,
    ) -> None:
        """Push a new sensor reading for a device."""
        # Fetch current device state
        resp = self.session.get(
            f"{BASE_URL}/entity/{DATABASE}", params={"key": device_id}
        )
        if resp.status_code == 404:
            device = {"device_id": device_id, "latest": {}, "history": []}
        else:
            resp.raise_for_status()
            device = resp.json()

        now = datetime.now(timezone.utc).isoformat()

        # Update latest reading
        reading = {
            "timestamp":   now,
            "temperature": temperature,
            "humidity":    humidity,
        }
        if pressure is not None:
            reading["pressure"] = pressure
        if battery_pct is not None:
            reading["battery_pct"] = battery_pct

        device["latest"] = reading

        # Append to rolling history (keep last MAX_HISTORY entries)
        device.setdefault("history", []).append(reading)
        if len(device["history"]) > MAX_HISTORY:
            device["history"] = device["history"][-MAX_HISTORY:]

        # Persist
        self.session.put(
            f"{BASE_URL}/entity/{DATABASE}", json={device_id: device}
        ).raise_for_status()
        print(f"[{device_id}] {now[:19]}  temp={temperature}°C  hum={humidity}%")

    def get_latest(self, device_id: str) -> Optional[dict]:
        """Get the most recent reading for a device."""
        resp = self.session.get(
            f"{BASE_URL}/entity/{DATABASE}", params={"key": device_id}
        )
        if resp.status_code == 404:
            return None
        resp.raise_for_status()
        return resp.json().get("latest")

    def get_history(self, device_id: str, last_n: int = 10) -> list[dict]:
        """Get the last N readings for a device."""
        resp = self.session.get(
            f"{BASE_URL}/entity/{DATABASE}", params={"key": device_id}
        )
        if resp.status_code == 404:
            return []
        resp.raise_for_status()
        return resp.json().get("history", [])[-last_n:]


if __name__ == "__main__":
    client = IoTClient()

    # Register sensors
    client.register_device("living_room",  "Living Room Sensor",  "Living Room")
    client.register_device("outdoor",      "Outdoor Station",     "Garden")
    client.register_device("server_rack",  "Server Room Monitor", "Basement")

    # Simulate readings coming in
    import random, time
    readings = [
        ("living_room", 21.5, 45.0),
        ("outdoor",     15.2, 78.0),
        ("server_rack", 28.0, 30.0),
        ("living_room", 21.8, 44.5),
        ("outdoor",     14.9, 80.0),
        ("living_room", 22.1, 43.0),
    ]

    print("\n--- Pushing readings ---")
    for device_id, temp, hum in readings:
        client.push_reading(device_id, temp, hum, battery_pct=random.uniform(60, 100))
        time.sleep(0.1)

    # Check latest readings
    print("\n--- Current readings ---")
    for device_id in ["living_room", "outdoor", "server_rack"]:
        latest = client.get_latest(device_id)
        if latest:
            print(f"  {device_id}: {latest['temperature']}°C, {latest['humidity']}% humidity")

    # Temperature history for living room
    print("\n--- Living room history ---")
    for reading in client.get_history("living_room"):
        print(f"  {reading['timestamp'][:19]}  {reading['temperature']}°C")
```

---

## curl: Push a Reading

```bash
BASE="http://127.0.0.1:8080"
TOKEN=$(curl -sf -X POST "$BASE/api/login" \
  -H 'Content-Type: application/json' \
  -d '{"client_id":"iot"}' | jq -r .token)
DEVICE="outdoor_sensor_01"
NOW=$(date -u +"%Y-%m-%dT%H:%M:%SZ")

# Fetch existing state (or start fresh)
CURRENT=$(curl -sf "$BASE/entity/iotdb?key=$DEVICE" \
  -H "Authorization: Bearer $TOKEN" 2>/dev/null \
  || echo '{"device_id":"'$DEVICE'","latest":{},"history":[]}')

# Append new reading
UPDATED=$(echo "$CURRENT" | jq \
  --arg ts "$NOW" \
  --argjson temp 16.3 \
  --argjson hum 72.5 \
  '.latest = {"timestamp":$ts,"temperature":$temp,"humidity":$hum}
   | .history += [{"timestamp":$ts,"temperature":$temp,"humidity":$hum}]
   | .history = .history[-100:]')  # keep last 100

curl -sf -X PUT "$BASE/entity/iotdb" \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d "{\"$DEVICE\": $UPDATED}" && echo "OK"

# Read latest
echo "--- Latest reading ---"
curl -sf "$BASE/entity/iotdb?key=$DEVICE" \
  -H "Authorization: Bearer $TOKEN" | jq .latest
```

---

## Alert Pattern

Add threshold alerting on the client side:

```python
def check_alerts(client: IoTClient, device_id: str) -> list[str]:
    """Return a list of active alerts for a device."""
    latest = client.get_latest(device_id)
    if not latest:
        return []

    alerts = []
    if latest.get("temperature", 0) > 35:
        alerts.append(f"HIGH TEMP: {latest['temperature']}°C")
    if latest.get("humidity", 0) > 85:
        alerts.append(f"HIGH HUMIDITY: {latest['humidity']}%")
    if latest.get("battery_pct", 100) < 15:
        alerts.append(f"LOW BATTERY: {latest['battery_pct']}%")
    return alerts


alerts = check_alerts(client, "server_rack")
if alerts:
    print(f"⚠️  Alerts for server_rack: {alerts}")
```

---

## High-Frequency Ingestion Tips

For high-frequency sensor data (> 1 reading/second per device):

1. **Batch readings** — collect N readings in memory and write them together in one PUT.
2. **Separate databases per device type** — `temp_sensors`, `motion_sensors`, etc.
3. **Increase `-cache-size`** — if you have many active devices, ensure the working set fits in cache.
4. **Use S3 storage** — for multi-region or multi-node deployments, S3 removes the need for a shared PVC.
