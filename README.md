# AJEAN

![Interface web d'AJEAN](docs/ui.png)

**Un seul binaire pour faire tourner [llama.cpp](https://github.com/ggml-org/llama.cpp) chez toi : il compile le backend pour ton matériel, tient le service, et te donne un chat web complet par-dessus.**

```
télécharge le binaire  →  jean llamacpp install  →  jean edit  →  jean start  →  c'est parti
```

Aucune dépendance à l'exécution, aucun flag CMake à retenir, aucun conteneur. Tu obtiens un endpoint compatible OpenAI et une interface web avec chat, mémoire, accès internet et outils.

---

## Ce que ça fait

**Le backend, tout seul.** `jean llamacpp install` clone et compile llama.cpp avec les bons flags pour *cette* machine : CUDA (capacité de calcul détectée par GPU, donc le multi-GPU marche), ROCm, Metal, Vulkan, ou repli CPU. `jean llamacpp update` récupère le dernier commit, arrête le service le temps de recompiler, et le redémarre.

**Le service.** `jean install` écrit l'unité systemd, la règle sudoers et les dossiers. Ensuite `start` / `stop` / `status` / `logs`. Sous Windows et macOS, voir plus bas.

**Le chat web** (`jean web`, ou `jean app` pour ouvrir le navigateur d'un coup). Changement de modèle et de preset, réglages d'apparence synchronisés entre appareils, prompt système éditable, téléchargement de modèles depuis un lien `.gguf`, et un compactage automatique du contexte quand la conversation devient longue.

**Le mode agent** (`jean agent on`) — un seul interrupteur qui donne à l'IA ses vrais outils :

| Outil | Rôle |
|---|---|
| terminal | exécute une commande (bash sous Unix, `cmd.exe` sous Windows) |
| write / edit | écrit un fichier, ou le modifie par remplacement exact |
| mem_* | mémoire Markdown persistante entre les sessions |
| web_* | recherche et lecture de pages, via Crawl4AI |
| mcp__* | les outils de tes serveurs MCP |

**Accès distant chiffré** (`jean link`) — une connexion sortante vers [ajean.link](https://ajean.link), donc aucun port à ouvrir, et ça marche en CGNAT. Le chat est chiffré de bout en bout : le relais ne voit jamais tes conversations.

## Démarrage

### 1. Le binaire

```bash
curl -L -o jean https://github.com/nathaninline/jean/releases/latest/download/jean-linux-amd64
chmod +x jean
sudo mv jean /usr/local/bin/jean
```

(Autres cibles sur la page [Releases](../../releases) : Linux/macOS/Windows, amd64 et arm64.)

### 2. Installe et compile

```bash
sudo jean install        # unité systemd, sudoers, dossiers
jean llamacpp install    # compile llama.cpp pour ton GPU
```

Nécessite `git` et `cmake`, plus le toolkit de ton accélérateur (CUDA, ROCm…) pour le GPU.

### 3. Démarre

```bash
jean edit      # règle MODEL=/chemin/vers/ton-modele.gguf
jean start
jean test      # vérifie que le modèle répond
jean web       # http://<hôte>:8090
```

## Commandes

```
Application :
  app                           lance l'UI web + ouvre le navigateur (auto au double-clic)
  web [PORT]                    UI web seule (défaut :8090)
  chat [prompt-système]         chat dans le terminal

Service :
  start | stop | restart        gérer le service
  status | logs                 état / logs en direct
  enable | disable              démarrage au boot
  edit                          éditer $JEAN_HOME/config.env
  test | bench [N]              vérifier que ça répond / mesurer les tok/s
  vram | gpu [index…]           VRAM / choix des GPU (gpu all = tous)
  set-api-key [clé]             protéger llama-server (Bearer)
  set-web-key [clé]             protéger l'API de pilotage

Capacités de l'IA :
  agent [on|off|status]         active TOUS les outils (terminal, fichiers, mémoire)
  memory [off|ondemand|always]  mode mémoire
  internet [on|off|url <url>|key <clé>]   accès web via Crawl4AI

Presets et backend :
  switch [N]                    changer de preset (configs/)
  llamacpp install|update|status

Accès distant (ajean.link) :
  link <token>                  enregistre le token et démarre le lien
  link start|restart|stop       gérer le service de lien
  link code                     code d'appairage (10 min, usage unique)
  link status|logout

Installation :
  install | uninstall
  update [--check]              se mettre à jour depuis les releases GitHub
  version
```

## Configuration

Tout vit sous **`$JEAN_HOME`** (`/etc/jean` sous Linux/macOS, `%ProgramData%\jean` sous Windows). Le service lit `config.env` :

| Clé | Signification | Défaut |
|-----|---------------|--------|
| `BIN` | chemin vers `llama-server` (réglé par `llamacpp install`) | — |
| `MODEL` | nom de fichier ou chemin complet du `.gguf` | — |
| `HOST` / `PORT` | adresse / port d'écoute | `0.0.0.0` / `8080` |
| `CTX` | taille du contexte | `32768` |
| `NGL` | couches déportées sur le GPU | `999` |
| `BATCH` / `UBATCH` | batch / micro-batch | `2048` / `512` |
| `THREADS` / `THREADS_BATCH` | threads CPU | `0` (auto) |
| `KV_TYPE` (`_K` / `_V`) | quantization du cache KV | — |
| `CUDA_VISIBLE_DEVICES` | GPU utilisés (réglé par `jean gpu`) | tous |
| `REASONING` | passthrough du mode raisonnement | — |
| `REASONING_BUDGET` | plafond de tokens de réflexion ; `-1` = illimité | `-1` |
| `COMPACT` | compactage automatique du contexte (`off` pour couper) | activé |
| `MEM_MODE` | mémoire : `off` / `ondemand` / `always` | `always` |
| `CRAWL4AI_URL` / `CRAWL4AI_KEY` | serveur d'accès internet | — |
| `EXTRA_ARGS` | ajouté tel quel à `llama-server` | — |

La clé API (`jean set-api-key`) vit dans `$JEAN_HOME/.api_key`, à part, pour survivre aux changements de preset.

**Modèles sur un autre disque.** Les `.gguf` n'ont pas à vivre dans `$JEAN_HOME` : dans l'éditeur de preset de l'UI, section *Modèle → Dossiers de modèles*, ajoute le dossier de ton disque externe. Ses modèles apparaissent dans la liste, groupés par dossier. Enregistré dans `model_dirs.json`, donc conservé d'un preset à l'autre.

### Variables d'environnement

| Variable | Rôle | Défaut |
|----------|------|--------|
| `JEAN_HOME` | racine des données | `/etc/jean`, `%ProgramData%\jean` |
| `JEAN_MODEL_DIRS` | dossiers de modèles (`:`, `;` sous Windows) | — |
| `JEAN_SERVICE` | nom de l'unité systemd | `jean` |
| `HF_TOKEN` | token Hugging Face pour les modèles privés | — |
| `JEAN_DL_CONNS` | connexions parallèles au téléchargement | — |
| `EDITOR` | éditeur pour `jean edit` | `nano` / `notepad` |

## Les outils de l'IA

### Mémoire

L'IA tient des pages Markdown sous `$JEAN_HOME/MEMORY/`, relues et mises à jour entre les sessions. Trois modes, indépendants du mode agent :

```bash
jean memory always     # (défaut) elle cherche avant de répondre et sauve d'elle-même
jean memory ondemand   # outils dispo, mais seulement quand tu le demandes
jean memory off        # coupée
```

### Accès internet

Par défaut l'IA n'a pas le web. En branchant un serveur [Crawl4AI](https://github.com/unclecode/crawl4ai), elle gagne `web_search` (DuckDuckGo), `web_open`, `web_read` et `web_grep`. **AJEAN ne fournit pas le serveur, il s'y branche :**

```bash
docker run -d -p 11235:11235 --shm-size=1g unclecode/crawl4ai:latest
jean internet url http://localhost:11235
jean internet on
jean internet status
```

Les outils web ne sont proposés au modèle que si le mode agent est actif, l'accès internet activé **et** le serveur joignable — sinon ils n'existent pas, donc il ne peut pas les inventer.

### Serveurs MCP

AJEAN parle le [Model Context Protocol](https://modelcontextprotocol.io) : tu branches des serveurs tiers (fichiers, bases de données, API…) et leurs outils s'ajoutent à ceux de l'IA, nommés `mcp__<serveur>__<outil>`.

La configuration se fait dans l'interface web (section *Serveurs MCP*) ou en éditant `$JEAN_HOME/mcp.json`, au **même format que Claude Desktop** — un fichier existant se réutilise tel quel :

```json
{
  "mcpServers": {
    "fs": { "command": "npx", "args": ["-y", "@modelcontextprotocol/server-filesystem", "/data"] },
    "api": { "url": "https://exemple.com/mcp" }
  }
}
```

Les transports **stdio** et **HTTP** sont gérés. Comme le terminal, un serveur MCP exécute du code sur ta machine : ses outils ne sont donnés au modèle que si le mode agent est actif.

## Windows

- **Pas de systemd** : `jean start` lance le service en arrière-plan (suivi par fichier PID), `stop` / `restart` / `status` / `logs` agissent dessus, sans droits administrateur. `enable` / `disable` ne sont pas gérés — passe par une tâche planifiée.
- `JEAN_HOME` vaut `%ProgramData%\jean` (repli `%LOCALAPPDATA%\jean`).
- `jean install` crée seulement le dossier de données et un `config.env` de départ.
- Le terminal de l'IA passe par `cmd.exe`. Elle le sait, et elle écrit ses fichiers par l'outil dédié plutôt que par le shell — c'est ce qui lui permet de produire des scripts contenant des guillemets.

```powershell
jean install
jean edit          # BIN=...\llama-server.exe et MODEL=...\modele.gguf
jean start
jean status
```

`jean llamacpp install` compile aussi sous Windows si `git` et `cmake` sont là ; sinon récupère un `llama-server.exe` pré-compilé et pointe `BIN` dessus.

## macOS

La page [Releases](../../releases) publie `Jean-macos-arm64.zip` / `Jean-macos-amd64.zip`, un bundle **`Jean.app`**. Dézippe, glisse dans *Applications*, ouvre : l'UI démarre sur `http://localhost:8090`, s'ouvre dans le navigateur, et l'icône se pose dans la **barre de menus**. Pas de Terminal, pas d'icône dans le Dock.

L'app n'est signée qu'en ad-hoc : au premier lancement, **clic droit → Ouvrir**.

Pour la ligne de commande, prends le binaire nu `jean-darwin-arm64` : hors bundle, il se comporte en CLI classique. Les services passent par **launchd**.

## Accès distant via ajean.link

`jean link` ouvre une connexion **sortante** vers le relais : ton serveur reste injoignable de l'extérieur, mais tu y accèdes de partout.

```bash
jean link <token>        # token fourni sur ajean.link
jean link code           # code d'appairage à saisir dans le portail
```

Le lien tourne comme un service : la commande rend la main, la connexion continue en tâche de fond. Pas besoin de `jean web` — l'UI est servie dans le tunnel. Tu retrouves depuis le portail l'interface de ton serveur avec un chat chiffré, la gestion de plusieurs machines, et en option un endpoint compatible OpenAI.

C'est un service optionnel et payant ; tout le reste d'AJEAN est et restera open source et gratuit.

### Sécurité — boîte noire

Le relais est conçu comme un **tube aveugle** : il transporte tes données sans pouvoir les lire.

- **Chat chiffré de bout en bout** (X25519 + AES-GCM). La clé est dérivée de ton mot de passe via **OPAQUE** et ne quitte jamais ton navigateur.
- **Empreinte vérifiée.** `jean link` affiche l'empreinte de la clé de la machine, à confirmer une fois dans le portail — ça défait toute interception par le relais.
- **Appairage authentifié.** Un code à usage unique (`jean link code`) garantit que seul *ton* navigateur pilote le serveur ; même compromis, le relais ne peut pas forger de commande.
- **Code servi hors du relais.** Le portail vient d'une origine indépendante (GitHub Pages) : le relais ne peut pas injecter de code pour voler ta clé.

Reste visible du relais : des métadonnées techniques (machine en ligne, modèle chargé, VRAM) — jamais tes conversations.

### Endpoint OpenAI (opt-in)

Pour brancher des outils tiers, AJEAN expose au besoin `https://<machine>.oai.ajean.link/v1`, authentifié par la clé de ton `llama-server`. **Désactivé par défaut**, activable par machine depuis l'UI (panneau *Accès OpenAI*), sans redémarrage.

Le VPS fait un simple **passthrough SNI** : le TLS est terminé sur *ta* machine (Let's Encrypt via TLS-ALPN-01, à travers le tunnel), le relais ne voit que du chiffré.

## API de pilotage

`jean web` expose une API HTTP pour piloter AJEAN à distance. Protège-la avant de l'exposer :

```bash
jean set-web-key      # génère une clé
```

Chaque appel `/api/*` présente alors `Authorization: Bearer <clé>` :

| Méthode | Endpoint | Rôle |
|---------|----------|------|
| GET  | `/api/ping` | connectivité + validité de la clé |
| GET  | `/api/status` · `/api/vram` | état du service · GPU |
| GET  | `/api/presets` | liste des presets (avec l'actif) |
| POST | `/api/switch` `{"n":<index>}` | changer de modèle |
| POST | `/api/start` · `/api/stop` · `/api/restart` | piloter le service |
| POST | `/api/chat` `{"messages":[…]}` | chat (flux SSE) |

> ⚠️ La clé voyage en clair en HTTP. Pour une exposition publique, mets un reverse-proxy HTTPS devant, ou utilise `jean link`.

## Compiler depuis les sources

Go 1.25+. AJEAN est 100 % Go, l'UI est embarquée via `go:embed` :

```bash
git clone https://github.com/nathaninline/jean.git
cd jean
CGO_ENABLED=0 go build -o jean ./cmd/jean
```

> Compiler **AJEAN** ne demande que Go. Compiler le **backend llama.cpp** demande `git`, `cmake` et le toolkit de ton accélérateur.

## Arborescence

- `cmd/jean/` — point d'entrée + ressources Windows (icône, versioninfo).
- `internal/jean/` — tout le code, fichiers préfixés par domaine (`web_*`, `chat_*`, `llm_*`, `backend_*`, `relay_*`, `sys_*`, `mcp_*`) ; carte dans `doc.go`.
- `internal/jean/ui/` — UI web embarquée. **`index.html` est généré** : les sources vivent dans `ui/src/`. Pour modifier l'interface, éditer `ui/src/` puis lancer `go generate ./internal/jean`.

## Licence

[MIT](LICENSE). Le `marked.min.js` embarqué est [Marked](https://github.com/markedjs/marked), également MIT.
