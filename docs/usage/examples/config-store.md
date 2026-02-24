---
layout: default
title: Configuration Store
parent: Examples
grand_parent: Usage
nav_order: 4
---

# Example: Configuration Store

Use DeltaDatabase as a **secure, versioned configuration store** for microservices. Each service reads its own configuration entity at startup and optionally polls for changes.

**Use case:** Feature flags, per-environment config (dev/staging/prod), per-tenant overrides, A/B test parameters.

**Database:** `configdb`  
**Entity key:** `"{service}.{environment}"` (e.g., `api-gateway.production`)  
**Entity value:** arbitrary service configuration object

---

## Schema Setup

Schemas are optional for config stores — you can use a permissive schema that accepts any object:

```bash
TOKEN=$(curl -s -X POST http://127.0.0.1:8080/api/login \
  -H 'Content-Type: application/json' \
  -d '{"client_id":"admin"}' | jq -r .token)

# Permissive schema — any JSON object is valid
curl -s -X PUT http://127.0.0.1:8080/schema/config.v1 \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{
    "$schema": "http://json-schema.org/draft-07/schema#",
    "$id": "config.v1",
    "type": "object"
  }'
```

Or enforce a typed structure:

```json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "$id": "service_config.v1",
  "type": "object",
  "properties": {
    "service":     {"type": "string"},
    "environment": {"type": "string", "enum": ["development", "staging", "production"]},
    "enabled":     {"type": "boolean"},
    "rate_limit":  {"type": "integer", "minimum": 0},
    "log_level":   {"type": "string", "enum": ["debug", "info", "warn", "error"]},
    "features":    {"type": "object"},
    "updated_at":  {"type": "string", "format": "date-time"}
  },
  "required": ["service", "environment"]
}
```

---

## Go Client

```go
package main

import (
    "bytes"
    "encoding/json"
    "fmt"
    "log"
    "net/http"
    "net/url"
    "time"
)

const (
    baseURL  = "http://127.0.0.1:8080"
    database = "configdb"
)

// ServiceConfig is the configuration for a single service + environment.
type ServiceConfig struct {
    Service     string                 `json:"service"`
    Environment string                 `json:"environment"`
    Enabled     bool                   `json:"enabled"`
    RateLimit   int                    `json:"rate_limit"`
    LogLevel    string                 `json:"log_level"`
    Features    map[string]interface{} `json:"features"`
    UpdatedAt   time.Time              `json:"updated_at"`
}

type ConfigClient struct {
    httpClient *http.Client
    token      string
}

func NewConfigClient(clientID string) (*ConfigClient, error) {
    c := &ConfigClient{httpClient: &http.Client{}}
    body, _ := json.Marshal(map[string]string{"client_id": clientID})
    resp, err := c.httpClient.Post(baseURL+"/api/login", "application/json",
        bytes.NewReader(body))
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()
    var result struct{ Token string `json:"token"` }
    json.NewDecoder(resp.Body).Decode(&result)
    c.token = result.Token
    return c, nil
}

func configKey(service, environment string) string {
    return service + "." + environment
}

func (c *ConfigClient) Get(service, environment string) (*ServiceConfig, error) {
    key := url.QueryEscape(configKey(service, environment))
    req, _ := http.NewRequest(http.MethodGet,
        fmt.Sprintf("%s/entity/%s?key=%s", baseURL, database, key), nil)
    req.Header.Set("Authorization", "Bearer "+c.token)

    resp, err := c.httpClient.Do(req)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()

    if resp.StatusCode == http.StatusNotFound {
        return nil, nil
    }

    var cfg ServiceConfig
    return &cfg, json.NewDecoder(resp.Body).Decode(&cfg)
}

func (c *ConfigClient) Set(cfg ServiceConfig) error {
    cfg.UpdatedAt = time.Now().UTC()
    entityJSON, _ := json.Marshal(cfg)
    key := configKey(cfg.Service, cfg.Environment)
    payload, _ := json.Marshal(map[string]json.RawMessage{key: entityJSON})

    req, _ := http.NewRequest(http.MethodPut,
        fmt.Sprintf("%s/entity/%s", baseURL, database),
        bytes.NewReader(payload))
    req.Header.Set("Authorization", "Bearer "+c.token)
    req.Header.Set("Content-Type", "application/json")

    resp, err := c.httpClient.Do(req)
    if err != nil {
        return err
    }
    defer resp.Body.Close()
    if resp.StatusCode != http.StatusOK {
        return fmt.Errorf("set config failed: status %d", resp.StatusCode)
    }
    return nil
}

func main() {
    client, err := NewConfigClient("config-manager")
    if err != nil {
        log.Fatal(err)
    }

    // Write configs for different environments
    configs := []ServiceConfig{
        {
            Service:     "api-gateway",
            Environment: "development",
            Enabled:     true,
            RateLimit:   1000,
            LogLevel:    "debug",
            Features:    map[string]interface{}{"new_ui": true, "beta_api": true},
        },
        {
            Service:     "api-gateway",
            Environment: "production",
            Enabled:     true,
            RateLimit:   100,
            LogLevel:    "warn",
            Features:    map[string]interface{}{"new_ui": false, "beta_api": false},
        },
    }

    for _, cfg := range configs {
        if err := client.Set(cfg); err != nil {
            log.Fatalf("set config: %v", err)
        }
        fmt.Printf("Saved config: %s.%s\n", cfg.Service, cfg.Environment)
    }

    // Read back
    prodCfg, err := client.Get("api-gateway", "production")
    if err != nil {
        log.Fatal(err)
    }
    fmt.Printf("\nProduction config:\n")
    fmt.Printf("  rate_limit: %d\n", prodCfg.RateLimit)
    fmt.Printf("  log_level:  %s\n", prodCfg.LogLevel)
    fmt.Printf("  features:   %v\n", prodCfg.Features)
}
```

---

## curl Walkthrough

```bash
BASE="http://127.0.0.1:8080"
TOKEN=$(curl -sf -X POST "$BASE/api/login" \
  -H 'Content-Type: application/json' \
  -d '{"client_id":"ops"}' | jq -r .token)

# Write production config
curl -sf -X PUT "$BASE/entity/configdb" \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{
    "payment-service.production": {
      "service":     "payment-service",
      "environment": "production",
      "enabled":     true,
      "rate_limit":  50,
      "log_level":   "warn",
      "features": {
        "stripe_v2":  true,
        "paypal":     false,
        "apple_pay":  true
      }
    }
  }'

# Read it back
curl -sf "$BASE/entity/configdb?key=payment-service.production" \
  -H "Authorization: Bearer $TOKEN" | jq .

# Enable PayPal
CURRENT=$(curl -sf "$BASE/entity/configdb?key=payment-service.production" \
  -H "Authorization: Bearer $TOKEN")
UPDATED=$(echo "$CURRENT" | jq '.features.paypal = true')
curl -sf -X PUT "$BASE/entity/configdb" \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d "{\"payment-service.production\": $UPDATED}"
echo "PayPal enabled"
```

---

## Feature Flag Pattern

Store feature flags per service with per-tenant overrides:

```python
import requests

BASE_URL = "http://127.0.0.1:8080"
token = requests.post(f"{BASE_URL}/api/login",
                      json={"client_id": "flag-service"}).json()["token"]
headers = {"Authorization": f"Bearer {token}"}

def get_flags(service: str, tenant_id: str | None = None) -> dict:
    """
    Get feature flags for a service.
    Tenant overrides take precedence over service defaults.
    """
    # Base flags for the service
    resp = requests.get(f"{BASE_URL}/entity/configdb",
                        params={"key": f"{service}.flags"}, headers=headers)
    base_flags = resp.json().get("flags", {}) if resp.status_code == 200 else {}

    if tenant_id is None:
        return base_flags

    # Per-tenant overrides
    resp = requests.get(f"{BASE_URL}/entity/configdb",
                        params={"key": f"{service}.flags.{tenant_id}"}, headers=headers)
    tenant_flags = resp.json().get("flags", {}) if resp.status_code == 200 else {}

    return {**base_flags, **tenant_flags}  # tenant overrides win


# Set global flags
requests.put(f"{BASE_URL}/entity/configdb", headers=headers, json={
    "recommendation-engine.flags": {
        "flags": {"new_algorithm": False, "personalization": True, "ab_test_v2": False}
    }
}).raise_for_status()

# Set per-tenant override (enable new algorithm for tenant "acme")
requests.put(f"{BASE_URL}/entity/configdb", headers=headers, json={
    "recommendation-engine.flags.acme": {
        "flags": {"new_algorithm": True, "ab_test_v2": True}
    }
}).raise_for_status()

# Check flags
print("Default flags:", get_flags("recommendation-engine"))
print("ACME flags:   ", get_flags("recommendation-engine", "acme"))
```

**Output:**

```
Default flags: {'new_algorithm': False, 'personalization': True, 'ab_test_v2': False}
ACME flags:    {'new_algorithm': True, 'personalization': True, 'ab_test_v2': True}
```

---

## Config Polling Pattern

Services can poll for configuration changes:

```python
import time
import threading

def poll_config(service: str, env: str, interval_s: int = 30):
    """Poll DeltaDatabase for config changes every `interval_s` seconds."""
    current_config = {}

    while True:
        resp = requests.get(
            f"{BASE_URL}/entity/configdb",
            params={"key": f"{service}.{env}"},
            headers=headers,
        )
        if resp.status_code == 200:
            new_config = resp.json()
            if new_config != current_config:
                print(f"Config changed for {service}.{env}: {new_config}")
                current_config = new_config
                apply_config(new_config)  # your application logic here

        time.sleep(interval_s)


def apply_config(config: dict) -> None:
    """Apply the new configuration to the running service."""
    print(f"Applying: rate_limit={config.get('rate_limit')}, "
          f"log_level={config.get('log_level')}")


# Start polling in a background thread
threading.Thread(target=poll_config, args=("api-gateway", "production"), daemon=True).start()
```
