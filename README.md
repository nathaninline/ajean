# Jean

**A single-binary manager for self-hosted [llama.cpp](https://github.com/ggml-org/llama.cpp) servers — plus a built-in web UI, terminal chat, and an auto-detecting backend builder.**

Drop one binary on a machine, run `jean llamacpp install`, and Jean clones, configures, and compiles llama.cpp for *that* machine's hardware (CUDA / ROCm / Metal / Vulkan / CPU) — no flags to remember. Then `jean start` and you have an OpenAI-compatible endpoint with a web chat on top.

```
download binary  →  jean llamacpp install  →  jean edit  →  jean start  →  done
```

---

## Why

Running llama.cpp as a real service usually means: figure out the right CMake flags for your GPU, write a systemd unit, manage an API key, swap models, keep the build up to date… Jean turns all of that into a handful of subcommands behind a single static binary with **no runtime dependencies** (other than llama.cpp itself, which Jean can build for you).

## Features

- **`jean llamacpp install` / `update`** — clones and compiles llama.cpp with the right flags **auto-detected** for the host:
  - **CUDA** when an NVIDIA GPU + `nvcc` are present (compute capability detected per-GPU via `nvidia-smi`, so multi-GPU machines build for all cards)
  - **ROCm/HIP** (AMD), **Metal** (macOS / Apple Silicon), **Vulkan**, or **CPU** fallback
  - `update` pulls the latest commit, stops the service while it rebuilds, then restarts it
- **systemd integration** — `jean install` writes the unit, a passwordless `systemctl` sudoers rule, and the data dirs
- **Web UI** (`jean web`) — chat, model/preset switching, skills & tools toggles
- **Terminal chat** (`jean chat`) — streamed responses
- **Presets** (`jean switch`) — keep multiple `config.env` profiles and swap between them
- **API key protection** (`jean set-api-key`) — Bearer auth for exposing the server publicly; the key is stored separately so it survives preset switches
- **Benchmark** (`jean bench`) — honest prefill/decode tok/s using a varied corpus
- **Single static binary** — built with `CGO_ENABLED=0`, cross-compiles trivially

## Quick start

### 1. Get the binary

Grab a prebuilt binary from the [Releases](../../releases) page, or [build from source](#building-from-source):

```bash
# example: Linux x86_64
curl -L -o jean https://github.com/jean-llm/jean/releases/latest/download/jean-linux-amd64
chmod +x jean
sudo mv jean /usr/local/bin/jean
```

### 2. Install (systemd unit, dirs, sudoers)

```bash
sudo jean install
```

### 3. Build a llama.cpp backend for this machine

```bash
jean llamacpp install
```

Jean auto-detects your accelerator, compiles `llama-server`, and points the config at the new binary. Requires `git` and `cmake` (plus the matching toolkit, e.g. CUDA, if you want GPU acceleration).

### 4. Point it at a model and start

```bash
jean edit      # set MODEL=/path/to/your-model.gguf
jean start
jean test      # verify the model answers
```

### 5. (optional) Web UI

```bash
jean web        # http://<host>:8090
```

## Commands

```
Service:
  start | stop | restart        manage the systemd service
  status | logs                 status / live logs
  enable | disable              start on boot
  edit                          edit $JEAN_HOME/config.env
  set-api-key [key]             protect the API (Bearer); empty = generate, "" = remove
  vram                          GPU/VRAM usage (nvidia-smi)
  test                          check the model answers (health + completion)
  bench [N]                     measure prefill + decode tok/s

Presets:
  switch [N]                    pick a preset from configs/ (interactive or by number)

Interaction:
  chat [system-prompt]          streamed terminal chat
  web [PORT]                    web UI (default :8090)

LLM-side tooling:
  skills [on|off|list]          let the model read SKILLS/<name>/SKILL.md
  tools  [on|off|status]        enable run_shell (model executes shell commands)

Backend (llama.cpp):
  llamacpp install              clone + build llama.cpp (auto-detect CUDA/ROCm/Metal/CPU), set BIN
  llamacpp update               git pull + rebuild the existing backend (stops/restarts the service)
  llamacpp status               current commit, detected backend, commits behind origin

Install:
  install                       install (systemd unit, sudoers, dirs)
  uninstall                     uninstall
```

### `jean llamacpp` flags

```
install [--dir=PATH] [--ref=GIT_REF] [--force] [--no-switch]
update  [--ref=GIT_REF] [--clean] [--no-restart] [--force]
```

- `--dir=` — where to clone (default `$JEAN_HOME/backends/llama.cpp`)
- `--ref=` — build a specific branch/tag/commit
- `--clean` — wipe `build/` and recompile from scratch
- `--no-switch` — don't touch `config.env` (install only)
- `--no-restart` — leave the service stopped after updating

## Configuration

Everything lives under **`$JEAN_HOME`** (default `/etc/jean`). The service reads `config.env`:

| Key | Meaning | Default |
|-----|---------|---------|
| `BIN` | path to `llama-server` (set by `llamacpp install`) | — |
| `MODEL` | path to the `.gguf` model | — |
| `HOST` / `PORT` | bind address / port | `0.0.0.0` / `8080` |
| `CTX` | context size | `32768` |
| `NGL` | GPU layers to offload | `999` |
| `BATCH` / `UBATCH` | batch / micro-batch | `2048` / `512` |
| `THREADS` / `THREADS_BATCH` | CPU threads | `0` (auto) |
| `KV_TYPE` (`_K`/`_V`) | KV cache quantization | — |
| `REASONING` | reasoning mode passthrough | — |
| `EXTRA_ARGS` | appended verbatim to `llama-server` | — |

The API key (when set with `jean set-api-key`) is stored in `$JEAN_HOME/.api_key`, separate from `config.env`.

### Environment

| Var | Meaning | Default |
|-----|---------|---------|
| `JEAN_HOME` | data root | `/etc/jean` (or `$HOME/JEAN`) |
| `JEAN_SERVICE` | systemd unit name | `jean` |
| `EDITOR` | editor for `jean edit` | `nano` |

## Building from source

Requires Go 1.22+. Jean is a pure-Go binary (the web UI is embedded via `go:embed`):

```bash
git clone https://github.com/jean-llm/jean.git
cd jean
CGO_ENABLED=0 go build -o jean .

# cross-compile, e.g. Linux from any host:
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o jean-linux-amd64 .
```

> Building **Jean** needs only Go. Building the **llama.cpp backend** (`jean llamacpp install`) needs `git`, `cmake`, and your accelerator's toolkit (CUDA, ROCm, etc.).

## How it works

- `jean serve` is the systemd `ExecStart`: it reads `config.env`, builds the `llama-server` argument list, and `exec`s into it so systemd supervises llama.cpp directly.
- `jean llamacpp` manages the llama.cpp checkout next to wherever `BIN` points, handling the common "relocated build dir" CMake-cache pitfall and stopping the service during a rebuild to avoid *Text file busy*.

## License

[MIT](LICENSE). The bundled `ui/marked.min.js` is [Marked](https://github.com/markedjs/marked), also MIT.
