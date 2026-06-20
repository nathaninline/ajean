# Jean

**Un gestionnaire mono-binaire pour serveurs [llama.cpp](https://github.com/ggml-org/llama.cpp) auto-hébergés — avec une interface web intégrée, un chat terminal, et un compilateur de backend qui détecte automatiquement le matériel.**

Dépose un seul binaire sur une machine, lance `jean llamacpp install`, et Jean clone, configure et compile llama.cpp pour le matériel de *cette* machine (CUDA / ROCm / Metal / Vulkan / CPU) — aucun flag à retenir. Ensuite `jean start` et tu as un endpoint compatible OpenAI avec un chat web par-dessus.

```
télécharge le binaire  →  jean llamacpp install  →  jean edit  →  jean start  →  c'est parti
```

---

## Pourquoi

Faire tourner llama.cpp comme un vrai service, ça veut dire d'habitude : trouver les bons flags CMake pour ton GPU, écrire une unité systemd, gérer une clé API, changer de modèle, garder le build à jour… Jean transforme tout ça en quelques sous-commandes derrière un binaire statique unique, **sans dépendance à l'exécution** (à part llama.cpp lui-même, que Jean peut compiler pour toi).

## Fonctionnalités

- **`jean llamacpp install` / `update`** — clone et compile llama.cpp avec les bons flags **détectés automatiquement** pour l'hôte :
  - **CUDA** quand un GPU NVIDIA + `nvcc` sont présents (compute capability détectée par GPU via `nvidia-smi`, donc les machines multi-GPU compilent pour toutes les cartes)
  - **ROCm/HIP** (AMD), **Metal** (macOS / Apple Silicon), **Vulkan**, ou repli **CPU**
  - `update` récupère le dernier commit, arrête le service le temps de recompiler, puis le redémarre
- **Intégration systemd** — `jean install` écrit l'unité, une règle sudoers `systemctl` sans mot de passe, et les dossiers de données
- **Interface web** (`jean web`) — chat, changement de modèle/preset, activation des skills & tools
- **Chat terminal** (`jean chat`) — réponses en streaming
- **Accès distant** (`jean link`) — connecte ce serveur à [ajean.link](https://ajean.link) par une connexion **sortante** (aucun port à ouvrir, marche même en CGNAT) : retrouve l'UI web et un endpoint **compatible OpenAI** depuis n'importe où
- **Presets** (`jean switch`) — garde plusieurs profils `config.env` et bascule entre eux
- **Protection par clé API** (`jean set-api-key`) — auth Bearer pour exposer le serveur publiquement ; la clé est stockée à part pour survivre aux changements de preset
- **Benchmark** (`jean bench`) — tok/s prefill/decode honnêtes avec un corpus varié
- **Binaire statique unique** — compilé avec `CGO_ENABLED=0`, se cross-compile trivialement

## Démarrage rapide

### 1. Récupère le binaire

Prends un binaire pré-compilé depuis la page [Releases](../../releases), ou [compile depuis les sources](#compiler-depuis-les-sources) :

```bash
# exemple : Linux x86_64
curl -L -o jean https://github.com/nathaninline/jean/releases/latest/download/jean-linux-amd64
chmod +x jean
sudo mv jean /usr/local/bin/jean
```

### 2. Installe (unité systemd, dossiers, sudoers)

```bash
sudo jean install
```

### 3. Compile un backend llama.cpp pour cette machine

```bash
jean llamacpp install
```

Jean détecte ton accélérateur, compile `llama-server`, et pointe la config sur le nouveau binaire. Nécessite `git` et `cmake` (plus le toolkit correspondant, ex. CUDA, si tu veux l'accélération GPU).

### 4. Pointe-le sur un modèle et démarre

```bash
jean edit      # règle MODEL=/chemin/vers/ton-modele.gguf
jean start
jean test      # vérifie que le modèle répond
```

### 5. (optionnel) Interface web

```bash
jean web        # http://<hôte>:8090
```

## Windows

Jean fonctionne aussi sur Windows. Les différences avec Linux :

- **Pas de systemd.** `jean start` lance `jean serve` en arrière-plan (processus détaché), suivi via un fichier PID. `stop` / `restart` / `status` / `logs` agissent dessus — aucun droit administrateur requis. `enable` / `disable` (démarrage au boot) ne sont pas gérés : utilise une tâche planifiée ou `sc.exe` si tu en as besoin.
- **`JEAN_HOME`** vaut par défaut `%ProgramData%\jean` (repli `%LOCALAPPDATA%\jean`). Surcharge avec la variable d'environnement `JEAN_HOME`.
- **`jean install`** crée seulement le dossier de données et un `config.env` de départ (pas de sudoers ni de symlink). Ajoute toi-même le dossier de `jean.exe` au `PATH` pour l'appeler de partout.
- **`jean edit`** ouvre `notepad` par défaut (surchargeable via `%EDITOR%`).
- **`jean tools`** exécute les commandes via `cmd /C` (au lieu de `bash`).

```powershell
# depuis PowerShell
jean install                 # crée %ProgramData%\jean\config.env
jean edit                    # règle BIN=...\llama-server.exe et MODEL=...\modele.gguf
jean start
jean status
jean logs                    # suit %ProgramData%\jean\jean.log
```

`jean llamacpp install` peut compiler llama.cpp si `git` et `cmake` sont présents ; sinon récupère un binaire `llama-server.exe` pré-compilé et pointe `BIN` dessus avec `jean edit`.

## Commandes

```
Service :
  start | stop | restart        gérer le service systemd
  status | logs                 état / logs en direct
  enable | disable              démarrage au boot
  edit                          éditer $JEAN_HOME/config.env
  set-api-key [clé]             protéger llama-server (Bearer) ; vide = générer, "" = retirer
  set-web-key [clé]             protéger l'API de pilotage 'jean web' ; vide = générer, "" = retirer
  vram                          utilisation GPU/VRAM (nvidia-smi)
  gpu [index…]                  liste les GPU / choisit le(s)quel(s) utiliser (gpu all = tous)
  test                          vérifie que le modèle répond (health + completion)
  bench [N]                     mesure prefill + decode tok/s

Presets :
  switch [N]                    choisir un preset dans configs/ (interactif ou par numéro)

Interaction :
  chat [system-prompt]          chat terminal streamé
  web [PORT]                    interface web (défaut :8090)

Accès distant (ajean.link) :
  link <token>                  connecte ce Jean au relais (accès web + API OpenAI depuis partout)
  link status | logout          état du lien / oublier le token

Outils côté LLM :
  skills [on|off|list]          laisse le modèle lire SKILLS/<nom>/SKILL.md
  machine [on|off|status]       active l'accès machine (le modèle dispose d'un shell complet sur le serveur)

Backend (llama.cpp) :
  llamacpp install              clone + compile llama.cpp (détecte CUDA/ROCm/Metal/CPU), règle BIN
  llamacpp update               git pull + recompile le backend existant (arrête/redémarre le service)
  llamacpp status               commit courant, backend détecté, commits de retard sur origin

Installation :
  install                       installer (unité systemd, sudoers, dossiers)
  uninstall                     désinstaller
```

### Options de `jean llamacpp`

```
install [--dir=CHEMIN] [--ref=REF_GIT] [--force] [--no-switch]
update  [--ref=REF_GIT] [--clean] [--no-restart] [--force]
```

- `--dir=` — où cloner (défaut `$JEAN_HOME/backends/llama.cpp`)
- `--ref=` — compiler une branche/tag/commit précis
- `--clean` — vide `build/` et recompile de zéro
- `--no-switch` — ne touche pas à `config.env` (install seulement)
- `--no-restart` — laisse le service arrêté après la mise à jour

## Configuration

Tout vit sous **`$JEAN_HOME`** (défaut `/etc/jean` sur Linux/macOS, `%ProgramData%\jean` sur Windows). Le service lit `config.env` :

| Clé | Signification | Défaut |
|-----|---------------|--------|
| `BIN` | chemin vers `llama-server` (réglé par `llamacpp install`) | — |
| `MODEL` | chemin vers le modèle `.gguf` | — |
| `HOST` / `PORT` | adresse / port d'écoute | `0.0.0.0` / `8080` |
| `CTX` | taille du contexte | `32768` |
| `NGL` | couches à déporter sur le GPU | `999` |
| `BATCH` / `UBATCH` | batch / micro-batch | `2048` / `512` |
| `THREADS` / `THREADS_BATCH` | threads CPU | `0` (auto) |
| `CUDA_VISIBLE_DEVICES` | GPU à utiliser (réglé par `jean gpu`) | tous |
| `KV_TYPE` (`_K`/`_V`) | quantization du cache KV | — |
| `REASONING` | passthrough du mode raisonnement | — |
| `EXTRA_ARGS` | ajouté tel quel à `llama-server` | — |

La clé API (quand elle est définie avec `jean set-api-key`) est stockée dans `$JEAN_HOME/.api_key`, séparément de `config.env`.

### API de pilotage à distance

`jean web` expose une API HTTP pour piloter Jean à distance : status, VRAM, liste/sélection de presets (switch de modèle), démarrage/arrêt/redémarrage du service, chat. Pour l'exposer sur internet en sécurité, protège-la par une clé :

```
jean set-web-key            # génère une clé aléatoire
jean web 8090               # sert l'API/UI sur :8090
```

Chaque appel `/api/*` doit alors présenter la clé (la page HTML/JS, elle, reste publique car sans secret) :

```
Authorization: Bearer <clé>
```

Endpoints utiles pour un client :

| Méthode | Endpoint | Rôle |
|---------|----------|------|
| GET  | `/api/ping` | vérifie connectivité + validité de la clé (200 / 401) |
| GET  | `/api/status` | état du service (active, health, port) |
| GET  | `/api/vram` | usage GPU/VRAM |
| GET  | `/api/presets` | liste des presets (avec l'actif) |
| POST | `/api/switch` `{"n":<index 1-based>}` | switch de modèle/preset |
| POST | `/api/start` · `/api/stop` · `/api/restart` | piloter le service |
| POST | `/api/chat` `{"messages":[…]}` | chat (flux SSE) |

La clé est stockée dans `$JEAN_HOME/.web_key`, distincte de `.api_key` (pilotage ≠ accès complétions), et relue à chaud à chaque requête. Le pilotage du service est cross-platform (systemd sous Linux, supervision par PID-file sous Windows).

> ⚠️ La clé voyage en clair en HTTP. Pour une exposition publique, place Jean derrière un reverse-proxy HTTPS (Caddy, nginx) ou un tunnel (Tailscale, Cloudflare Tunnel).

### Accès distant via ajean.link

Plutôt que d'exposer un port, `jean link` ouvre une connexion **sortante** vers le relais [ajean.link](https://ajean.link) : ton serveur reste injoignable depuis l'extérieur, mais tu y accèdes quand même depuis n'importe où — idéal derrière une box ou en CGNAT.

```bash
jean web                 # l'UI doit tourner localement
jean link <token>        # token fourni sur ajean.link
```

Une fois lié, tu retrouves depuis le portail :
- l'**interface web** de ton serveur, à distance ;
- un **endpoint compatible OpenAI** (`https://ajean.link/oai/<machine>/v1`, clé = ton token) pour brancher n'importe quel outil (OpenCode, etc.) ;
- la gestion de **plusieurs serveurs** et d'**agents** depuis un tableau de bord.

C'est un service optionnel et payant ; tout le reste de Jean est et restera open source et gratuit.

### Variables d'environnement

| Variable | Signification | Défaut |
|----------|---------------|--------|
| `JEAN_HOME` | racine des données | `/etc/jean` (ou `$HOME/JEAN`) |
| `JEAN_SERVICE` | nom de l'unité systemd | `jean` |
| `EDITOR` | éditeur pour `jean edit` | `nano` |

## Compiler depuis les sources

Nécessite Go 1.22+. Jean est un binaire 100 % Go (l'UI web est embarquée via `go:embed`) :

```bash
git clone https://github.com/nathaninline/jean.git
cd jean
CGO_ENABLED=0 go build -o jean .

# cross-compilation, ex. Linux depuis n'importe quel hôte :
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o jean-linux-amd64 .
```

> Compiler **Jean** ne nécessite que Go. Compiler le **backend llama.cpp** (`jean llamacpp install`) nécessite `git`, `cmake`, et le toolkit de ton accélérateur (CUDA, ROCm, etc.).

## Comment ça marche

- `jean serve` est l'`ExecStart` de systemd : il lit `config.env`, construit la liste d'arguments de `llama-server`, et fait un `exec` dessus pour que systemd supervise directement llama.cpp.
- `jean llamacpp` gère le checkout llama.cpp à côté de l'endroit où pointe `BIN`, en gérant le piège classique du « dossier de build relocalisé » (cache CMake) et en arrêtant le service pendant la recompilation pour éviter le *Text file busy*.

## Licence

[MIT](LICENSE). Le fichier `ui/marked.min.js` embarqué est [Marked](https://github.com/markedjs/marked), également MIT.
