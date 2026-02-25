
# Example: Chat Application

Store AI/LLM conversation histories for a chat application backend. Each user session is stored as a separate entity with its full message history.

**Use case:** You're building a chatbot or AI assistant and need to persist conversation state between user sessions.

**Database:** `chatdb`  
**Entity key:** session ID (e.g., `session_001`)  
**Entity value:** `{"messages": [{"role": "user"|"assistant"|"system", "content": "..."}]}`

---

## Schema Setup

First, define the schema for chat messages:

```bash
TOKEN=$(curl -s -X POST http://127.0.0.1:8080/api/login \
  -H 'Content-Type: application/json' \
  -d '{"client_id":"admin"}' | jq -r .token)

curl -s -X PUT http://127.0.0.1:8080/schema/chat.v1 \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{
    "$schema": "http://json-schema.org/draft-07/schema#",
    "$id": "chat.v1",
    "type": "object",
    "properties": {
      "messages": {
        "type": "array",
        "items": {
          "type": "object",
          "properties": {
            "role":    {"type": "string", "enum": ["user", "assistant", "system"]},
            "content": {"type": "string"}
          },
          "required": ["role", "content"]
        }
      }
    },
    "required": ["messages"]
  }'
```

---

## Go Client

A complete Go client that stores and retrieves multi-turn conversations:

```go
package main

import (
    "bytes"
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "net/url"
)

const (
    baseURL  = "http://127.0.0.1:8080"
    database = "chatdb"
)

type Message struct {
    Role    string `json:"role"`
    Content string `json:"content"`
}

type Session struct {
    Messages []Message `json:"messages"`
}

type ChatClient struct {
    httpClient *http.Client
    baseURL    string
    token      string
}

func NewChatClient(baseURL, clientID string) (*ChatClient, error) {
    c := &ChatClient{httpClient: &http.Client{}, baseURL: baseURL}
    if err := c.login(clientID); err != nil {
        return nil, err
    }
    return c, nil
}

func (c *ChatClient) login(clientID string) error {
    body, _ := json.Marshal(map[string]string{"client_id": clientID})
    resp, err := c.httpClient.Post(c.baseURL+"/api/login",
        "application/json", bytes.NewReader(body))
    if err != nil {
        return err
    }
    defer resp.Body.Close()
    var result struct{ Token string `json:"token"` }
    if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
        return err
    }
    c.token = result.Token
    return nil
}

func (c *ChatClient) doRequest(method, path string, body io.Reader) (*http.Response, error) {
    req, err := http.NewRequest(method, c.baseURL+path, body)
    if err != nil {
        return nil, err
    }
    req.Header.Set("Authorization", "Bearer "+c.token)
    if body != nil {
        req.Header.Set("Content-Type", "application/json")
    }
    return c.httpClient.Do(req)
}

func (c *ChatClient) GetSession(sessionID string) (Session, error) {
    path := fmt.Sprintf("/entity/%s?key=%s", database, url.QueryEscape(sessionID))
    resp, err := c.doRequest(http.MethodGet, path, nil)
    if err != nil {
        return Session{}, err
    }
    defer resp.Body.Close()
    if resp.StatusCode == http.StatusNotFound {
        return Session{}, nil
    }
    var session Session
    return session, json.NewDecoder(resp.Body).Decode(&session)
}

func (c *ChatClient) AppendMessage(sessionID string, msg Message) error {
    session, err := c.GetSession(sessionID)
    if err != nil {
        return fmt.Errorf("get session: %w", err)
    }
    session.Messages = append(session.Messages, msg)

    entityJSON, _ := json.Marshal(session)
    payload, _ := json.Marshal(map[string]json.RawMessage{sessionID: entityJSON})

    path := fmt.Sprintf("/entity/%s", database)
    resp, err := c.doRequest(http.MethodPut, path, bytes.NewReader(payload))
    if err != nil {
        return err
    }
    defer resp.Body.Close()
    if resp.StatusCode != http.StatusOK {
        return fmt.Errorf("put failed with status %d", resp.StatusCode)
    }
    return nil
}

func main() {
    client, err := NewChatClient(baseURL, "demo-app")
    if err != nil {
        panic(err)
    }
    fmt.Println("Logged in successfully")

    sessionID := "session_001"
    turns := []Message{
        {Role: "user",      Content: "Hello! Can you help me with Go?"},
        {Role: "assistant", Content: "Of course! What would you like to know?"},
        {Role: "user",      Content: "How do I read a file?"},
        {Role: "assistant", Content: "Use os.ReadFile(path) — it returns []byte and an error."},
    }

    for _, msg := range turns {
        if err := client.AppendMessage(sessionID, msg); err != nil {
            panic(err)
        }
        fmt.Printf("Stored [%s]: %q\n", msg.Role, msg.Content)
    }

    session, err := client.GetSession(sessionID)
    if err != nil {
        panic(err)
    }
    fmt.Printf("\n=== Session %s (%d messages) ===\n", sessionID, len(session.Messages))
    for _, m := range session.Messages {
        fmt.Printf("  [%-9s] %s\n", m.Role, m.Content)
    }
}
```

**Expected output:**

```
Logged in successfully
Stored [user]:      "Hello! Can you help me with Go?"
Stored [assistant]: "Of course! What would you like to know?"
Stored [user]:      "How do I read a file?"
Stored [assistant]: "Use os.ReadFile(path) — it returns []byte and an error."

=== Session session_001 (4 messages) ===
  [user     ] Hello! Can you help me with Go?
  [assistant] Of course! What would you like to know?
  [user     ] How do I read a file?
  [assistant] Use os.ReadFile(path) — it returns []byte and an error.
```

---

## Python Client

```python
"""
chat_client.py — DeltaDatabase chat backend example

Install: pip install requests
"""
import requests

BASE_URL = "http://127.0.0.1:8080"
DATABASE = "chatdb"


class DeltaChatClient:
    def __init__(self, client_id: str) -> None:
        self.session = requests.Session()
        resp = self.session.post(f"{BASE_URL}/api/login", json={"client_id": client_id})
        resp.raise_for_status()
        token = resp.json()["token"]
        self.session.headers["Authorization"] = f"Bearer {token}"
        print(f"Logged in as '{client_id}'")

    def get_session(self, session_id: str) -> dict:
        resp = self.session.get(f"{BASE_URL}/entity/{DATABASE}", params={"key": session_id})
        if resp.status_code == 404:
            return {"messages": []}
        resp.raise_for_status()
        return resp.json()

    def append_message(self, session_id: str, role: str, content: str) -> None:
        data = self.get_session(session_id)
        data.setdefault("messages", []).append({"role": role, "content": content})
        resp = self.session.put(f"{BASE_URL}/entity/{DATABASE}", json={session_id: data})
        resp.raise_for_status()

    def print_session(self, session_id: str) -> None:
        data = self.get_session(session_id)
        messages = data.get("messages", [])
        print(f"\n=== Session '{session_id}' ({len(messages)} messages) ===")
        for m in messages:
            print(f"  [{m['role']:<9}] {m['content']}")


if __name__ == "__main__":
    client = DeltaChatClient("python-demo")
    session_id = "py_session_001"

    conversation = [
        ("user",      "What is DeltaDatabase?"),
        ("assistant", "An encrypted JSON database written in Go."),
        ("user",      "Does it support schemas?"),
        ("assistant", "Yes — JSON Schema draft-07 validation on every write."),
    ]

    for role, content in conversation:
        client.append_message(session_id, role, content)
        print(f"  stored [{role}]: {content!r}")

    client.print_session(session_id)
```

---

## curl / Shell Script

```bash
#!/usr/bin/env bash
BASE="http://127.0.0.1:8080"
DB="chatdb"
SESSION="bash_session_001"

# 1. Login
TOKEN=$(curl -sf -X POST "$BASE/api/login" \
  -H 'Content-Type: application/json' \
  -d '{"client_id":"bash-demo"}' | jq -r .token)

# Helper: append a message
append_message() {
  local role="$1" content="$2"
  existing=$(curl -sf "$BASE/entity/$DB?key=$SESSION" \
    -H "Authorization: Bearer $TOKEN" 2>/dev/null || echo '{"messages":[]}')
  updated=$(echo "$existing" | jq \
    --arg r "$role" --arg c "$content" \
    '.messages += [{"role":$r,"content":$c}]')
  curl -sf -X PUT "$BASE/entity/$DB" \
    -H "Authorization: Bearer $TOKEN" \
    -H 'Content-Type: application/json' \
    -d "{\"$SESSION\": $updated}" > /dev/null
  echo "  stored [$role]: $content"
}

# 2. Build conversation
append_message "user"      "How do I start DeltaDatabase?"
append_message "assistant" "Run bin/main-worker and bin/proc-worker, then POST /api/login."
append_message "user"      "Where is data stored?"
append_message "assistant" "In shared/db/files/ as AES-256-GCM encrypted blobs."

# 3. Read it back
echo ""
echo "=== Full session ==="
curl -sf "$BASE/entity/$DB?key=$SESSION" \
  -H "Authorization: Bearer $TOKEN" | jq .
```

---

## Integration with OpenAI / LangChain

Use DeltaDatabase as the memory backend for LLM-powered applications:

```python
import openai
import requests

BASE_URL = "http://127.0.0.1:8080"
DATABASE = "chatdb"

class DeltaMemory:
    """Use DeltaDatabase to persist LLM conversation history."""

    def __init__(self, session_id: str, token: str):
        self.session_id = session_id
        self.headers = {"Authorization": f"Bearer {token}"}

    def load(self) -> list[dict]:
        resp = requests.get(
            f"{BASE_URL}/entity/{DATABASE}",
            params={"key": self.session_id},
            headers=self.headers,
        )
        if resp.status_code == 404:
            return []
        return resp.json().get("messages", [])

    def save(self, messages: list[dict]) -> None:
        requests.put(
            f"{BASE_URL}/entity/{DATABASE}",
            json={self.session_id: {"messages": messages}},
            headers=self.headers,
        ).raise_for_status()


def chat(session_id: str, user_message: str, token: str) -> str:
    memory = DeltaMemory(session_id, token)

    # Load history
    messages = memory.load()
    messages.append({"role": "user", "content": user_message})

    # Call OpenAI
    response = openai.chat.completions.create(
        model="gpt-4o",
        messages=messages,
    )
    assistant_message = response.choices[0].message.content

    # Save updated history
    messages.append({"role": "assistant", "content": assistant_message})
    memory.save(messages)

    return assistant_message


# Usage
token = requests.post(
    f"{BASE_URL}/api/login", json={"client_id": "openai-app"}
).json()["token"]

reply = chat("user_123_session_1", "Explain AES-256-GCM in one sentence.", token)
print(reply)
```
