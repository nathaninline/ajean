<p align="center"><img src="docs/banner.svg" alt="AJEAN" width="100%"></p>

<p align="center"><strong>English</strong> · <a href="README.fr.md">Français</a></p>

> [!IMPORTANT]
> **AJEAN has changed.** It is no longer a local AI engine manager: it is now a **real personal assistant** that you connect to the API of your choice (OpenAI, Anthropic, Mistral, Ollama, LM Studio or any compatible API). It remembers you, schedules tasks, builds apps and makes its own tools.
>
> The previous version remains available, frozen, on the [`ajean-v0.18.17`](https://github.com/nathaninline/ajean/tree/ajean-v0.18.17) branch.

# AJEAN

**A personal assistant that remembers you.** One single executable: plug it into an OpenAI-compatible API and you're ready. No Docker, no Python, no Node to install.

## Getting started

Download the file for your system from [ajean.app](https://ajean.app) or from the [releases](https://github.com/nathaninline/ajean/releases) (Windows, macOS, Linux), then run it. Or build it yourself:

> [!TIP]
> **Choose its place first.** AJEAN stores all its data **next to the executable**. Before running it, create a folder for it (for example `Documents/AJEAN`), move the downloaded file there and run it from that folder: its memory, files and settings will be created there. Don't run it straight from your *Downloads* folder, or your data will end up there. To back up or move AJEAN later, just copy its whole folder.

```bash
go build -o ajean.exe ./cmd/ajean
```

```bash
./ajean.exe
```

The interface opens at `http://127.0.0.1:8090`. On first launch, the **Settings** window asks you to connect an API: provider (OpenAI, Anthropic, OpenRouter, Mistral, Groq, DeepSeek, Gemini, Ollama, LM Studio or any compatible URL), key and model. The *Test* button checks the connection before saving.

### Several APIs

- A **provider** is an API: a name, a URL, a key. Add as many as you like.
- A **model** is what you pick to chat with: a name ("Fast", "Thoughtful"…), a provider, the model ID at that provider and a reasoning effort. Switch with one click, below the input box.
- **Dictation** can use a transcription API (provider and model of your choice); without one, the browser's own is used.
- The **interface** is available in English, French and Vietnamese (Language setting). AJEAN itself answers in the language of your messages.

## What it can do

- **Remember.** A short profile (who you are), *notes* (procedures, recipes, lists kept in full), *lessons* (what it learned from your corrections), *writing rules* applied mechanically, and a log of every exchange. Memory is shared across all conversations.
- **Recall on its own.** Before each answer, whatever its memory holds that is relevant to your message is put in front of it: it doesn't have to think of searching.
- **Learn afterwards.** When you stop talking, a silent *review* rereads the exchange and keeps what deserves keeping (a fact, a correction, a procedure).
- **Sleep.** At night, a *sleep* rereads the whole memory and tidies it: duplicates merged, contradictions settled, notes tightened. A copy is taken before each sleep (restorable from the interface) and a safeguard puts back anything the cleanup would have lost.
- **Several conversations**, each saved as readable Markdown.
- **Tasks**: anything it must do later or regularly. A reminder ("remind me tomorrow at 9 to call the garage"), a watch ("every morning, check the flight price and tell me if it drops below €300"), a recurring job. The task runs on its own, away from your conversations, and its report arrives as a message from it, with a notification. The **Tasks** screen lets you create, pause, run and review them.
- **Apps**: "build me an app to track my expenses". It writes a small app you open from the **Apps** screen, and improves it on request. Every version is kept: you can roll back.
- **Work on files**, only inside its `WORKSPACE` folder: it cannot get out. The **Files** screen lets you browse, add, rename and delete them.
- **Drive a browser** (Chrome, Chromium, Edge or Brave): open a page, read it, click, type.
- **Make its own tools**: scripts it writes once, tests and reuses, stored in `TOOLS`.
- **Attachments and dictation**: drop files (images are shown to the model if it accepts them), dictate into the mic.

## Where the data lives

Everything lives next to the executable:

| Folder | Content |
|---|---|
| `MEMORY/` | the whole memory, in Markdown: `profile.md`, `lessons.md`, `rules.md`, `fiches/`, `journal/`, `conversations/`. Readable and editable by hand. |
| `WORKSPACE/` | the only folder where the assistant reads and writes files. Your attachments land in `uploads/`, its downloads in `downloads/`. |
| `TOOLS/` | the tools it made, one `.js` script per tool. |
| `APPS/` | the apps it built, with their versions and data. |
| `config.json` | providers (URLs, keys), models and preferences. |
| `.ajean/` | technical state: counters, scheduled tasks, pre-sleep copies, the driven browser's profile, the local embeddings engine and its model (`embedder/`, re-downloadable). |

Backing up AJEAN means copying this folder.

## What keeps it contained

- **Files**: every access goes through a root locked by the operating system (`os.Root`) on `WORKSPACE`. A `..` path, an absolute path or a symbolic link pointing outside are refused.
- **No system commands**: no tool runs a program on the machine. A command could not be contained.
- **Its tools** run in a JavaScript interpreter built into the binary. A script only has access to what it is given: the `WORKSPACE` files and the web. Nothing else exists for it.
- **Network**: neither its tools nor its browser can reach the machine itself (including its own interface) nor, by default, the local network.
- **Browser**: a dedicated profile (never yours), `http(s)` addresses only, downloads into `WORKSPACE/downloads`.
- **Apps**: an app is shown in a frame with no origin and no network. It can reach neither the Internet nor AJEAN; it only has its own data, the tools the assistant explicitly opened to it, and a question to the model.

## Who can open the interface

- **No password** (the default): only the machine itself opens the interface. A request coming from elsewhere, directly or through a proxy installed on the machine, is refused.
- **With a password**: everyone signs in, the machine included. Set it in the settings (General, Security) or with `ajean password`. `AJEAN_HOST` refuses to open the interface to the network until a password exists.
- **Forgotten password**: on the machine, run `ajean password` (or `ajean password --remove`). A running server takes it into account at once.
- **What is stored**: the password is derived (PBKDF2-SHA256, random salt), and a session is kept as the fingerprint of its token. Reading `.ajean/auth.json` lets nobody in.
- **Sessions**: a cookie the page's scripts cannot read (`HttpOnly`), never sent by another site (`SameSite=Strict`), reserved to HTTPS when the page is served over HTTPS. A session ends after 30 days without a visit. Changing the password closes all of them; the settings can sign out the other devices.
- **Guessing**: after 5 wrong passwords, an address waits 30 seconds, then twice as long after each new failure, up to an hour. A shared brake stops attempts spread over many addresses.
- **Over the Internet, use HTTPS**: either a reverse proxy in front (Caddy, nginx), or `AJEAN_TLS_CERT` and `AJEAN_TLS_KEY`. Over plain HTTP the password travels in clear text; the sign-in screen says so.

## The tools it makes

A tool is a `TOOLS/<name>.js` file that defines `function run(args)`. They are not sent to the model with every message: a single tool, `tools`, is always present, and that's how it lists, loads and runs them on demand. A thousand tools cost nothing in context.

```js
/* TOOL {"description":"Converts an amount in euros to another currency","params":{"amount":"Amount in euros","currency":"Currency code, e.g. USD"}} */
function run(args) {
  var r = http.get("https://api.frankfurter.app/latest?from=EUR&to=" + args.currency);
  var rate = JSON.parse(r.body).rates[args.currency];
  return args.amount + " EUR = " + (args.amount * rate).toFixed(2) + " " + args.currency;
}
```

Available in a script: `args`, `ws.read / write / list / exists / remove / move`, `http.get / post / request`, `console.log`, and standard JavaScript (`JSON`, `Math`, `Date`…).

## Advanced settings

Environment variables, all optional:

| Variable | Role | Default |
|---|---|---|
| `AJEAN_HOME` | data folder | the executable's folder |
| `AJEAN_HOST`, `AJEAN_PORT` | listen address | `127.0.0.1:8090` |
| `AJEAN_WEB_KEY` | password of the interface, when none has been saved | none |
| `AJEAN_TLS_CERT`, `AJEAN_TLS_KEY` | certificate and key (PEM files): the interface is served over HTTPS | |
| `AJEAN_API_URL`, `AJEAN_API_KEY`, `AJEAN_MODEL` | the API, instead of the settings | |
| `AJEAN_NO_BROWSER` | don't open the interface at startup | |
| `AJEAN_BROWSER` | browser the assistant drives (full path) | detected |
| `AJEAN_BROWSER_HEADLESS` | drive it without a window | |
| `AJEAN_TOOLS_LAN` | allow its tools and browser to reach the local network | |

**Search by meaning.** With it, log search and automatic recall find a memory even when worded differently. Without it, search is by words. Two ways to turn it on in the settings:

- **the local engine**: AJEAN downloads [llama.cpp](https://github.com/ggml-org/llama.cpp) itself (pinned version, checksum verified) and a small embeddings model from Hugging Face (EmbeddingGemma by default, 330 MB), then runs it on the CPU. Nothing leaves the machine. The engine starts on the first search and stops after twenty minutes of inactivity. Another GGUF model from Hugging Face can be given by its address;
- **an API**: a provider and an *embeddings model* (for example `text-embedding-3-small`).

## Code layout

```
cmd/ajean/           entry point
internal/
  home/              disk locations, atomic writes
  config/            settings
  llm/               API client (streamed chat, tools, embeddings)
  embedder/          search-by-meaning vectors: API or local engine (install, launch)
  tool/              the shape of a tool
  memory/            profile, lessons, rules, notes, log, search, recall, sleep copies
  workspace/         the locked working folder
  scripts/           the tools the assistant makes (built-in interpreter)
  tasks/             scheduled tasks: calendar, execution, reports
  apps/              apps built by the assistant: versions, sandboxed page, bridge
  browser/           browser driving
  netguard/          refusal of local addresses
  auth/              interface password, sessions, brake on guessing
  agent/             conversations, model ↔ tools loop, review, sleep
  server/            HTTP API and web interface (ui/)
```

```bash
go test ./...
```

## License

MIT.
