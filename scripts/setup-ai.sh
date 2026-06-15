#!/usr/bin/env bash
# One-command setup for Atlas Monitor's Assistant: make sure Ollama is installed
# and running, then pull the model the app is configured to use (default
# qwen2.5:3b). Safe to re-run — it only does what is missing.
#
# Usage:
#   scripts/setup-ai.sh [MODEL]        # or: make setup-ai
#   ATLAS_AI_MODEL=llama3.2 scripts/setup-ai.sh
set -euo pipefail

ollama_url="http://localhost:11434"

say()  { printf '\033[1m%s\033[0m\n' "$*"; }   # bold heading
info() { printf '  %s\n' "$*"; }

# --- Pick the model ----------------------------------------------------------
# Priority: explicit arg > $ATLAS_AI_MODEL > the app's saved settings > default.
default_model="qwen2.5:3b"
settings="${XDG_CONFIG_HOME:-$HOME/.config}/atlas-monitor/settings.json"
saved_model=""
if [ -f "$settings" ]; then
    saved_model="$(sed -n 's/.*"model"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$settings" | head -n1)"
fi
MODEL="${1:-${ATLAS_AI_MODEL:-${saved_model:-$default_model}}}"

# --- 1. Ensure Ollama is installed -------------------------------------------
if command -v ollama >/dev/null 2>&1; then
    info "Ollama is already installed ($(command -v ollama))."
else
    say "Ollama is not installed."
    info "The Assistant uses Ollama to run a language model locally on your machine."
    info "This will run the official installer: curl -fsSL https://ollama.com/install.sh | sh"
    if [ -t 0 ]; then
        printf '  Install Ollama now? [Y/n] '
        read -r reply
        case "$reply" in
            [Nn]*) echo "Aborted. Install Ollama yourself, then re-run this script."; exit 1 ;;
        esac
    fi
    curl -fsSL https://ollama.com/install.sh | sh
fi

# --- 2. Ensure the Ollama server is responding -------------------------------
reachable() { curl -fsS --max-time 3 "$ollama_url/api/tags" >/dev/null 2>&1; }

if ! reachable; then
    say "Starting the Ollama server…"
    # The official installer normally enables a systemd service; use it if present,
    # otherwise fall back to a backgrounded 'ollama serve'.
    if command -v systemctl >/dev/null 2>&1 \
       && systemctl list-unit-files 2>/dev/null | grep -q '^ollama\.service'; then
        sudo systemctl enable --now ollama 2>/dev/null || true
    fi
    if ! reachable; then
        info "Launching 'ollama serve' in the background (log: /tmp/atlas-ollama-serve.log)…"
        nohup ollama serve >/tmp/atlas-ollama-serve.log 2>&1 &
    fi
    for _ in $(seq 1 30); do            # wait up to ~15s for the API to come up
        reachable && break
        sleep 0.5
    done
fi

if ! reachable; then
    echo "Error: Ollama is installed but its API at $ollama_url is not responding." >&2
    echo "Start it manually with 'ollama serve' and re-run this script." >&2
    exit 1
fi
info "Ollama server is up at $ollama_url."

# --- 3. Pull the model -------------------------------------------------------
if ollama list 2>/dev/null | awk 'NR>1 {print $1}' | grep -qx "$MODEL"; then
    info "Model '$MODEL' is already installed."
else
    say "Pulling model '$MODEL' (this can be a few GB on first download)…"
    ollama pull "$MODEL"
fi

say "Done — open Atlas Monitor → Assistant and start asking questions."
info "To use a different model, change it in Settings (the gear) or re-run: scripts/setup-ai.sh <model>"
