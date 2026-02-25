"""
DeltaDatabase Chat Application
A Flask-based chat app that uses DeltaDatabase as its sole storage backend.
"""
import os
import uuid
import hashlib
import secrets
from datetime import datetime
from functools import wraps

import requests
from flask import (
    Flask,
    render_template,
    request,
    redirect,
    url_for,
    session,
    jsonify,
    flash,
)

app = Flask(__name__)
app.secret_key = os.environ.get("FLASK_SECRET_KEY", secrets.token_hex(32))

# ---------------------------------------------------------------------------
# DeltaDatabase connection
# ---------------------------------------------------------------------------
DELTA_DB_URL = os.environ.get("DELTA_DB_URL", "http://localhost:8080")
DELTA_DB_CLIENT_ID = os.environ.get("DELTA_DB_CLIENT_ID", "chat-app")

# Database / collection names inside DeltaDatabase
DB_USERS = "chat_users"
DB_CHATS = "chat_sessions"
DB_CHAT_INDEX = "chat_index"
DB_USER_CONFIG = "chat_user_config"
DB_ADMIN_CONFIG = "chat_admin_config"

DEFAULT_MODELS = ["gpt-4o", "gpt-4o-mini", "gpt-3.5-turbo"]

# Module-level token cache (refreshed on 401)
_delta_token: str | None = None


def _login_delta() -> str:
    resp = requests.post(
        f"{DELTA_DB_URL}/api/login",
        json={"client_id": DELTA_DB_CLIENT_ID},
        timeout=10,
    )
    resp.raise_for_status()
    return resp.json()["token"]


def get_delta_token() -> str:
    global _delta_token
    if not _delta_token:
        _delta_token = _login_delta()
    return _delta_token


def _headers() -> dict:
    return {"Authorization": f"Bearer {get_delta_token()}"}


def delta_get(db: str, key: str):
    """Return the entity dict or None if not found."""
    global _delta_token
    resp = requests.get(
        f"{DELTA_DB_URL}/entity/{db}",
        params={"key": key},
        headers=_headers(),
        timeout=10,
    )
    if resp.status_code == 401:
        _delta_token = _login_delta()
        resp = requests.get(
            f"{DELTA_DB_URL}/entity/{db}",
            params={"key": key},
            headers=_headers(),
            timeout=10,
        )
    if resp.status_code == 404:
        return None
    resp.raise_for_status()
    return resp.json()


def delta_put(db: str, key: str, value: dict) -> None:
    """Create or update an entity."""
    global _delta_token
    resp = requests.put(
        f"{DELTA_DB_URL}/entity/{db}",
        json={key: value},
        headers=_headers(),
        timeout=10,
    )
    if resp.status_code == 401:
        _delta_token = _login_delta()
        resp = requests.put(
            f"{DELTA_DB_URL}/entity/{db}",
            json={key: value},
            headers=_headers(),
            timeout=10,
        )
    resp.raise_for_status()


# ---------------------------------------------------------------------------
# Auth helpers
# ---------------------------------------------------------------------------

def hash_password(password: str) -> str:
    return hashlib.sha256(password.encode()).hexdigest()


def verify_password(password: str, stored_hash: str) -> bool:
    return secrets.compare_digest(hash_password(password), stored_hash)


def login_required(f):
    @wraps(f)
    def decorated(*args, **kwargs):
        if "username" not in session:
            return redirect(url_for("login"))
        return f(*args, **kwargs)

    return decorated


def admin_required(f):
    @wraps(f)
    def decorated(*args, **kwargs):
        if "username" not in session:
            return redirect(url_for("login"))
        if not session.get("is_admin"):
            flash("Admin access required.", "error")
            return redirect(url_for("chat_list"))
        return f(*args, **kwargs)

    return decorated


# ---------------------------------------------------------------------------
# Admin config helpers
# ---------------------------------------------------------------------------

def get_admin_config() -> dict:
    config = delta_get(DB_ADMIN_CONFIG, "global")
    if config is None:
        config = {"available_models": list(DEFAULT_MODELS), "user_models": {}}
        delta_put(DB_ADMIN_CONFIG, "global", config)
    return config


def get_user_models(username: str) -> list:
    config = get_admin_config()
    user_specific = config.get("user_models", {}).get(username)
    if user_specific:
        return user_specific
    return config.get("available_models", DEFAULT_MODELS)


# ---------------------------------------------------------------------------
# Routes – public
# ---------------------------------------------------------------------------

@app.route("/")
def index():
    if "username" in session:
        return redirect(url_for("chat_list"))
    return redirect(url_for("login"))


@app.route("/login", methods=["GET", "POST"])
def login():
    if "username" in session:
        return redirect(url_for("chat_list"))
    if request.method == "POST":
        username = request.form.get("username", "").strip()
        password = request.form.get("password", "")
        if not username or not password:
            flash("Username and password are required.", "error")
            return render_template("login.html")
        user = delta_get(DB_USERS, username)
        if not user or not verify_password(password, user.get("password_hash", "")):
            flash("Invalid username or password.", "error")
            return render_template("login.html")
        session["username"] = username
        session["is_admin"] = user.get("is_admin", False)
        return redirect(url_for("chat_list"))
    return render_template("login.html")


@app.route("/register", methods=["GET", "POST"])
def register():
    if "username" in session:
        return redirect(url_for("chat_list"))
    if request.method == "POST":
        username = request.form.get("username", "").strip()
        password = request.form.get("password", "")
        confirm = request.form.get("confirm_password", "")
        if not username or not password:
            flash("Username and password are required.", "error")
            return render_template("register.html")
        if password != confirm:
            flash("Passwords do not match.", "error")
            return render_template("register.html")
        if len(password) < 6:
            flash("Password must be at least 6 characters.", "error")
            return render_template("register.html")
        if delta_get(DB_USERS, username) is not None:
            flash("Username already taken.", "error")
            return render_template("register.html")
        now = datetime.utcnow().isoformat()
        delta_put(
            DB_USERS,
            username,
            {
                "password_hash": hash_password(password),
                "is_admin": False,
                "created_at": now,
            },
        )
        # Add to users index
        idx = delta_get(DB_ADMIN_CONFIG, "users_index") or {"users": []}
        if username not in idx["users"]:
            idx["users"].append(username)
        delta_put(DB_ADMIN_CONFIG, "users_index", idx)
        flash("Account created! Please log in.", "success")
        return redirect(url_for("login"))
    return render_template("register.html")


@app.route("/logout", methods=["POST"])
def logout():
    session.clear()
    return redirect(url_for("login"))


# ---------------------------------------------------------------------------
# Routes – chat
# ---------------------------------------------------------------------------

@app.route("/chat")
@login_required
def chat_list():
    username = session["username"]
    chats = _load_chat_list(username)
    return render_template("chat.html", chats=chats, current_chat=None, chat_id=None, models=[], user_config={})


@app.route("/chat/new", methods=["POST"])
@login_required
def new_chat():
    username = session["username"]
    chat_id = uuid.uuid4().hex[:10]
    now = datetime.utcnow().isoformat()
    chat_data = {
        "username": username,
        "id": chat_id,
        "title": "New Chat",
        "messages": [],
        "created_at": now,
        "updated_at": now,
    }
    delta_put(DB_CHATS, f"{username}__{chat_id}", chat_data)
    idx = delta_get(DB_CHAT_INDEX, username) or {"chats": []}
    idx["chats"].insert(0, chat_id)
    delta_put(DB_CHAT_INDEX, username, idx)
    return redirect(url_for("chat_view", chat_id=chat_id))


@app.route("/chat/<chat_id>")
@login_required
def chat_view(chat_id: str):
    username = session["username"]
    chat_data = _get_own_chat(username, chat_id)
    if chat_data is None:
        flash("Chat not found.", "error")
        return redirect(url_for("chat_list"))
    chats = _load_chat_list(username)
    models = get_user_models(username)
    user_config = delta_get(DB_USER_CONFIG, username) or {}
    return render_template(
        "chat.html",
        chats=chats,
        current_chat=chat_data,
        chat_id=chat_id,
        models=models,
        user_config=user_config,
    )


@app.route("/chat/<chat_id>/message", methods=["POST"])
@login_required
def send_message(chat_id: str):
    username = session["username"]
    data = request.get_json() or {}
    user_message = (data.get("message") or "").strip()
    model = data.get("model", "gpt-4o-mini")

    if not user_message:
        return jsonify({"error": "Message cannot be empty."}), 400

    allowed = get_user_models(username)
    if model not in allowed:
        model = allowed[0] if allowed else "gpt-4o-mini"

    chat_data = _get_own_chat(username, chat_id)
    if chat_data is None:
        return jsonify({"error": "Chat not found."}), 404

    user_config = delta_get(DB_USER_CONFIG, username) or {}
    api_key = user_config.get("openai_api_key") or os.environ.get("OPENAI_API_KEY", "")
    base_url = user_config.get("openai_base_url") or "https://api.openai.com/v1"

    # Support mock mode for testing without a real API key
    if os.environ.get("MOCK_OPENAI") == "true":
        assistant_message = f"[mock] You said: {user_message}"
    else:
        if not api_key:
            return jsonify({"error": "No OpenAI API key configured. Please add one in Settings."}), 400
        try:
            import openai
            client = openai.OpenAI(api_key=api_key, base_url=base_url)
            messages = chat_data.get("messages", [])
            messages.append({"role": "user", "content": user_message})
            response = client.chat.completions.create(model=model, messages=messages)
            assistant_message = response.choices[0].message.content
        except Exception as exc:
            return jsonify({"error": str(exc)}), 500

    messages = chat_data.get("messages", [])
    messages.append({"role": "user", "content": user_message})
    messages.append({"role": "assistant", "content": assistant_message})

    # Auto-title from first user turn
    if chat_data.get("title") == "New Chat":
        chat_data["title"] = (user_message[:50] + "…") if len(user_message) > 50 else user_message

    now = datetime.utcnow().isoformat()
    chat_data["messages"] = messages
    chat_data["updated_at"] = now
    delta_put(DB_CHATS, f"{username}__{chat_id}", chat_data)

    return jsonify({"reply": assistant_message, "title": chat_data["title"]})


@app.route("/chat/<chat_id>/delete", methods=["POST"])
@login_required
def delete_chat(chat_id: str):
    username = session["username"]
    idx = delta_get(DB_CHAT_INDEX, username) or {"chats": []}
    idx["chats"] = [c for c in idx["chats"] if c != chat_id]
    delta_put(DB_CHAT_INDEX, username, idx)
    # Overwrite with a tombstone so the key is gone from active chats
    now = datetime.utcnow().isoformat()
    delta_put(
        DB_CHATS,
        f"{username}__{chat_id}",
        {"username": username, "id": chat_id, "deleted": True,
         "title": "", "messages": [], "created_at": now, "updated_at": now},
    )
    return jsonify({"status": "ok"})


# ---------------------------------------------------------------------------
# Routes – settings
# ---------------------------------------------------------------------------

@app.route("/settings", methods=["GET", "POST"])
@login_required
def settings():
    username = session["username"]
    if request.method == "POST":
        api_key = request.form.get("openai_api_key", "").strip()
        base_url = (request.form.get("openai_base_url") or "https://api.openai.com/v1").strip()
        default_model = request.form.get("default_model", "gpt-4o-mini").strip()
        allowed = get_user_models(username)
        if default_model not in allowed:
            default_model = allowed[0] if allowed else "gpt-4o-mini"
        delta_put(
            DB_USER_CONFIG,
            username,
            {
                "openai_api_key": api_key,
                "openai_base_url": base_url,
                "default_model": default_model,
            },
        )
        flash("Settings saved!", "success")
        return redirect(url_for("settings"))
    user_config = delta_get(DB_USER_CONFIG, username) or {}
    models = get_user_models(username)
    return render_template("settings.html", config=user_config, models=models)


# ---------------------------------------------------------------------------
# Routes – admin
# ---------------------------------------------------------------------------

@app.route("/admin")
@admin_required
def admin():
    idx = delta_get(DB_ADMIN_CONFIG, "users_index") or {"users": []}
    users = []
    for uname in idx.get("users", []):
        udata = delta_get(DB_USERS, uname)
        if udata:
            users.append(
                {
                    "username": uname,
                    "is_admin": udata.get("is_admin", False),
                    "created_at": udata.get("created_at", ""),
                }
            )
    admin_config = get_admin_config()
    return render_template(
        "admin.html",
        users=users,
        admin_config=admin_config,
        default_models=DEFAULT_MODELS,
    )


@app.route("/admin/user/<username>/models", methods=["POST"])
@admin_required
def admin_user_models(username: str):
    data = request.get_json() or {}
    models = data.get("models", [])
    config = get_admin_config()
    config.setdefault("user_models", {})[username] = models
    delta_put(DB_ADMIN_CONFIG, "global", config)
    return jsonify({"status": "ok"})


@app.route("/admin/available-models", methods=["POST"])
@admin_required
def admin_available_models():
    data = request.get_json() or {}
    models = data.get("models", [])
    config = get_admin_config()
    config["available_models"] = models
    delta_put(DB_ADMIN_CONFIG, "global", config)
    return jsonify({"status": "ok"})


# ---------------------------------------------------------------------------
# Private helpers
# ---------------------------------------------------------------------------

def _load_chat_list(username: str) -> list:
    idx = delta_get(DB_CHAT_INDEX, username) or {"chats": []}
    chats = []
    for cid in idx.get("chats", []):
        cd = delta_get(DB_CHATS, f"{username}__{cid}")
        if cd and not cd.get("deleted"):
            chats.append(
                {
                    "id": cid,
                    "title": cd.get("title", "Untitled"),
                    "updated_at": cd.get("updated_at", ""),
                }
            )
    chats.sort(key=lambda x: x["updated_at"], reverse=True)
    return chats


def _get_own_chat(username: str, chat_id: str):
    """Return chat data only if it belongs to the given user, else None."""
    cd = delta_get(DB_CHATS, f"{username}__{chat_id}")
    if cd is None or cd.get("deleted") or cd.get("username") != username:
        return None
    return cd


# ---------------------------------------------------------------------------
# Bootstrap admin user on first run
# ---------------------------------------------------------------------------

def setup_admin_user() -> None:
    admin_username = os.environ.get("ADMIN_USERNAME", "admin")
    admin_password = os.environ.get("ADMIN_PASSWORD", "admin123")
    if delta_get(DB_USERS, admin_username) is None:
        now = datetime.utcnow().isoformat()
        delta_put(
            DB_USERS,
            admin_username,
            {
                "password_hash": hash_password(admin_password),
                "is_admin": True,
                "created_at": now,
            },
        )
        idx = delta_get(DB_ADMIN_CONFIG, "users_index") or {"users": []}
        if admin_username not in idx["users"]:
            idx["users"].append(admin_username)
        delta_put(DB_ADMIN_CONFIG, "users_index", idx)
        print(f"[chat-app] Created admin user '{admin_username}'")


if __name__ == "__main__":
    try:
        setup_admin_user()
    except Exception as exc:
        print(f"[chat-app] Warning – could not bootstrap admin: {exc}")
    app.run(
        host="0.0.0.0",
        port=int(os.environ.get("PORT", 5000)),
        debug=os.environ.get("FLASK_DEBUG", "false").lower() == "true",
    )
