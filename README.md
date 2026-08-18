# AJEAN

![Interface web d'AJEAN](docs/ui.png)

**Vos modèles d'IA tournent chez vous, dans un seul binaire : chat, mémoire persistante, accès web, outils et accès à distance chiffré.**

AJEAN fournit tout ce qui entoure le modèle : l'interface de chat, les outils de l'assistant, la gestion du service et celle du matériel. Le moteur d'inférence est [llama.cpp](https://github.com/ggml-org/llama.cpp), qu'AJEAN compile lui-même pour la machine sur laquelle il tourne.

```
télécharger le binaire  →  ajean llamacpp install  →  ajean edit  →  ajean start  →  c'est parti
```

Aucune dépendance à l'exécution, aucun flag CMake à retenir, aucun conteneur. Vous obtenez une interface de chat complète et un endpoint compatible OpenAI pour vos outils tiers.

---

## Ce que fait AJEAN

**Un assistant, pas seulement un modèle.** L'interface web offre un chat avec raisonnement affiché, mémoire persistante, compactage automatique du contexte quand la conversation s'allonge, prompt système éditable, et des réglages d'apparence synchronisés entre appareils.

**Des outils réels.** `ajean agent on` active d'un seul coup toutes les capacités du modèle sur la machine :

| Outil | Rôle |
|---|---|
| terminal | exécute une commande (bash sous Unix, `cmd.exe` sous Windows) |
| write / edit | écrit un fichier, ou le modifie par remplacement exact |
| mem_* | mémoire Markdown persistante entre les sessions |
| web_* | recherche et lecture de pages |
| mcp__* | les outils des serveurs MCP configurés |

**Le matériel et le moteur, gérés pour vous.** `ajean llamacpp install` clone et compile llama.cpp avec les bons flags pour *cette* machine : CUDA (capacité de calcul détectée par GPU, donc le multi-GPU fonctionne), ROCm, Metal, Vulkan, ou repli CPU. `ajean llamacpp update` récupère le dernier commit, arrête le service le temps de recompiler, puis le redémarre.

**Des services, pas des scripts.** `ajean install` écrit les deux unités systemd, les règles sudoers et les dossiers. Ensuite `start` / `stop` / `status` / `logs`. Windows et macOS ont leurs équivalents natifs (voir plus bas).

**Plusieurs modèles, un clic.** Les presets gardent chacun leur configuration complète ; basculer de l'un à l'autre recharge le modèle sans toucher à un fichier. Les `.gguf` peuvent vivre sur n'importe quel disque.

**Accessible de partout.** `ajean link` ouvre une connexion sortante vers [ajean.link](https://ajean.link), donc aucun port à ouvrir, et cela fonctionne même en CGNAT. Le chat est chiffré de bout en bout : le relais ne voit jamais les conversations.

## Démarrage

### 1. Le binaire

```bash
curl -L -o ajean https://github.com/nathaninline/ajean/releases/latest/download/ajean-linux
chmod +x ajean
sudo mv ajean /usr/local/bin/ajean
```

Les binaires publiés : `ajean-linux`, `ajean-linux-arm`, `ajean-macos`, `ajean-macos-arm`, `ajean-windows.exe`, `ajean-windows-arm.exe` — le suffixe `-arm` désigne l'arm64, l'absence de suffixe l'x86-64.

### 2. Installation et compilation du moteur

```bash
sudo ajean install        # deux unités systemd, sudoers, dossiers
ajean llamacpp install    # compile llama.cpp pour le GPU présent
```

Nécessite `git` et `cmake`, plus le toolkit de l'accélérateur (CUDA, ROCm…) pour l'accélération GPU.

### 3. Démarrage

```bash
ajean edit      # régler MODEL=/chemin/vers/le-modele.gguf
ajean start     # démarre le moteur (ajean-engine)
ajean test      # vérifier que le modèle répond
ajean ui start  # démarre l'interface (ajean-ui) sur http://<hôte>:8090
```

AJEAN tourne en **deux services** : `ajean-engine`, qui exécute le modèle, et
`ajean-ui`, qui sert l'interface web, le tunnel d'accès distant et l'endpoint
OpenAI. Les séparer permet de redémarrer l'interface — ce qui est instantané —
sans recharger des dizaines de gigaoctets de modèle.

## Commandes

```
Moteur (ajean-engine) :
  start | stop | restart        gérer le service
  status | logs                 état / logs en direct
  enable | disable              démarrage au boot
  edit                          éditer la configuration dans $EDITOR
  switch [N]                    changer de preset (presets/)
  test | bench [N]              vérifier que le modèle répond / mesurer les tok/s
  vram | gpu [index…]           VRAM / choix des GPU (gpu all = tous)
  set-api-key [clé]             protéger le moteur d'inférence (Bearer)
  llamacpp install|update|status

Interface (ajean-ui) :
  ui [start|stop|restart|status]  piloter le service d'interface
  web [PORT]                    servir l'interface au premier plan (défaut :8090)
  set-web-key [clé]             protéger l'API de pilotage

Capacités de l'IA :
  chat [prompt-système]         chat dans le terminal
  agent [on|off|status]         active TOUS les outils (terminal, fichiers, mémoire)
  memory [off|ondemand|always]  mode mémoire
  internet [on|off|engine <go|crawl4ai>|url <url>|key <clé>]   accès web

Accès distant (ajean.link) :
  link <token>                  enregistre le jeton et ouvre le tunnel
  link code                     code d'appairage (10 min, usage unique)
  link status|logout

Installation :
  install | uninstall
  update [--check]              mise à jour depuis les releases GitHub
  where | version
```

## Configuration

Tout vit sous **`$AJEAN_HOME`** (`/etc/ajean` sous Linux/macOS, `%ProgramData%\ajean` sous Windows) :

| | |
|---|---|
| `backends/` | llama.cpp, compilé ou téléchargé |
| `bin/` | le binaire installé (Windows) |
| `models/` | les `.gguf` |
| `presets/` | un `.env` par preset |
| `memory/` | les pages de mémoire de l'IA (`.md`) |
| `workspace/` | ce que l'IA écrit en mode agent |
| `ajean.db` | tout l'état : configuration, préférences, conversation, clés, interrupteurs |

S'y ajoutent à la racine les quelques fichiers qui ne peuvent pas aller ailleurs : `.e2e_key` (clé privée du chiffrement de bout en bout), `certs/` (certificats TLS gérés par certmagic), et les journaux et fichiers PID des services.

La base, un unique fichier [bbolt](https://github.com/etcd-io/bbolt), remplace la dizaine de fichiers d'état d'autrefois. Restent des fichiers ce qui se lit et s'édite à la main : les presets, les pages de mémoire et, bien sûr, les modèles.

La configuration du moteur s'édite avec `ajean edit`, qui la déroule au format `clé=valeur` dans `$EDITOR` :

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
| `CUDA_VISIBLE_DEVICES` | GPU utilisés (réglé par `ajean gpu`) | tous |
| `REASONING` | passthrough du mode raisonnement | — |
| `REASONING_BUDGET` | plafond de tokens de réflexion ; `-1` = illimité | `-1` |
| `COMPACT` | compactage automatique du contexte (`off` pour couper) | activé |
| `MEM_MODE` | mémoire : `off` / `ondemand` / `always` | `always` |
| `CRAWL4AI_URL` / `CRAWL4AI_KEY` | serveur d'accès internet | — |
| `EXTRA_ARGS` | ajouté tel quel à la ligne de commande du moteur | — |

La clé API (`ajean set-api-key`) est rangée hors de la configuration, afin de survivre aux changements de preset.

**Modèles sur un autre disque.** Les `.gguf` n'ont pas à résider dans `$AJEAN_HOME/models` : dans l'éditeur de preset de l'interface, section *Modèle → Dossiers de modèles*, ajoutez le dossier voulu. Ses modèles apparaissent dans la liste, groupés par dossier. La liste est enregistrée dans la base, donc conservée d'un preset à l'autre.

### Variables d'environnement

| Variable | Rôle | Défaut |
|----------|------|--------|
| `AJEAN_HOME` | racine des données | `/etc/ajean`, `%ProgramData%\ajean` |
| `AJEAN_MODEL_DIRS` | dossiers de modèles (séparés par `:`, `;` sous Windows) | — |
| `AJEAN_SERVICE` | nom de l'unité du moteur | `ajean-engine` |
| `HF_TOKEN` | token Hugging Face pour les modèles privés | — |
| `AJEAN_DL_CONNS` | connexions parallèles au téléchargement | — |
| `EDITOR` | éditeur pour `ajean edit` | `nano` / `notepad` |

## Les capacités de l'IA

### Mémoire

L'IA tient des pages Markdown sous `$AJEAN_HOME/memory/`, relues et mises à jour entre les sessions. Trois modes, indépendants du mode agent :

```bash
ajean memory always     # (défaut) elle cherche avant de répondre et enregistre d'elle-même
ajean memory ondemand   # outils disponibles, mais utilisés seulement sur demande
ajean memory off        # mémoire coupée
```

### Accès internet

Par défaut, l'IA n'a pas accès au web. Une fois activé, elle gagne `web_search` (DuckDuckGo), `web_open`, `web_read` et `web_grep`. Deux moteurs sont disponibles.

**Moteur intégré (défaut)** — inclus dans le binaire, rien à installer :

```bash
ajean internet on
ajean internet status
```

Il récupère les pages en HTTP, en extrait le contenu (Readability) et le convertit en markdown. Il n'exécute pas le JavaScript : une page entièrement rendue côté client ressort vide. Docs, articles, blogs, Wikipédia, GitHub et forums passent sans problème.

**Moteur Crawl4AI** — un serveur [Crawl4AI](https://github.com/unclecode/crawl4ai) que vous hébergez, avec Chromium headless, donc rendu JavaScript complet. **AJEAN ne fournit pas ce serveur, il s'y branche :**

```bash
docker run -d -p 11235:11235 --shm-size=1g unclecode/crawl4ai:latest
ajean internet engine crawl4ai
ajean internet url http://localhost:11235
ajean internet on
```

Les outils web ne sont proposés au modèle que si le mode agent est actif, l'accès internet activé **et** — avec Crawl4AI — le serveur joignable. Sinon ils n'existent pas, et le modèle ne peut donc pas les inventer.

### Serveurs MCP

AJEAN parle le [Model Context Protocol](https://modelcontextprotocol.io) : on y branche des serveurs tiers (fichiers, bases de données, API…) et leurs outils s'ajoutent à ceux de l'IA, nommés `mcp__<serveur>__<outil>`.

La configuration se fait depuis l'interface web (section *Serveurs MCP*) (section *Serveurs MCP*). Le format des serveurs est celui de Claude Desktop, si bien qu'une configuration existante se recopie telle quelle :

```json
{
  "mcpServers": {
    "fs": { "command": "npx", "args": ["-y", "@modelcontextprotocol/server-filesystem", "/data"] },
    "api": { "url": "https://exemple.com/mcp" }
  }
}
```

Les transports **stdio** et **HTTP** sont pris en charge. Comme le terminal, un serveur MCP exécute du code sur la machine hôte : ses outils ne sont donnés au modèle que si le mode agent est actif.

## Windows

- **Pas de systemd** : `ajean start` lance le service en arrière-plan (suivi par fichier PID) ; `stop`, `restart`, `status` et `logs` agissent dessus, sans droits administrateur. `enable` / `disable` ne sont pas gérés, il faut passer par une tâche planifiée.
- `AJEAN_HOME` vaut `%ProgramData%\ajean` (repli `%LOCALAPPDATA%\ajean`).
- `ajean install` crée seulement l'arborescence de données et une configuration de départ.
- Le terminal de l'IA passe par `cmd.exe`. Elle le sait, et écrit ses fichiers par l'outil dédié plutôt que par le shell, ce qui lui permet de produire des scripts contenant des guillemets.

```powershell
ajean install
ajean edit          # BIN=...\llama-server.exe et MODEL=...\modele.gguf
ajean start
ajean status
```

`ajean llamacpp install` compile également sous Windows si `git` et `cmake` sont présents ; sinon, récupérer un `llama-server.exe` pré-compilé et pointer `BIN` dessus.

## macOS

La page [Releases](../../releases) publie `ajean-macos-arm.zip` (Apple Silicon) et `ajean-macos.zip` (Intel), un bundle **`AJEAN.app`**. Dézipper, glisser dans *Applications*, ouvrir : l'interface démarre sur `http://localhost:8090`, s'ouvre dans le navigateur, et l'icône se pose dans la **barre de menus**. Pas de fenêtre de Terminal, pas d'icône dans le Dock.

L'application n'est signée qu'en ad-hoc : au premier lancement, faire **clic droit → Ouvrir**.

Pour un usage en ligne de commande, prendre le binaire nu `ajean-macos-arm` : hors bundle, il conserve son comportement CLI. Les services passent par **launchd**.

## Accès distant via ajean.link

`ajean link` ouvre une connexion **sortante** vers le relais : le serveur reste injoignable depuis l'extérieur, mais reste accessible de partout.

```bash
ajean link <token>        # token fourni sur ajean.link
ajean link code           # code d'appairage à saisir dans le portail
```

Le tunnel n'est pas un service à part : il est ouvert par `ajean-ui`, le service qui sert déjà l'interface, dès qu'un jeton est enregistré. Un seul process sert donc l'interface locale et l'accès distant — c'est ce qui garantit **une seule conversation**, identique sur les deux. Le portail donne accès à l'interface du serveur avec un chat chiffré, à la gestion de plusieurs machines, et en option à un endpoint compatible OpenAI.

Il s'agit d'un service optionnel et payant ; tout le reste d'AJEAN est et restera open source et gratuit.

### Sécurité : la boîte noire

Le relais est conçu comme un **tube aveugle** : il transporte les données sans pouvoir les lire.

- **Chat chiffré de bout en bout** (X25519 + AES-GCM). La clé est dérivée du mot de passe via **OPAQUE** et ne quitte jamais le navigateur.
- **Empreinte vérifiée.** `ajean link` affiche l'empreinte de la clé de la machine, à confirmer une fois dans le portail, ce qui défait toute tentative d'interception par le relais.
- **Appairage authentifié.** Un code à usage unique (`ajean link code`) garantit qu'un seul navigateur autorisé pilote le serveur ; même compromis, le relais ne peut pas forger de commande.
- **Code servi hors du relais.** Le portail provient d'une origine indépendante (GitHub Pages) : le relais ne peut pas injecter de code pour dérober la clé.

Reste visible du relais : des métadonnées techniques (machine en ligne, modèle chargé, VRAM), jamais le contenu des conversations.

### Endpoint OpenAI (opt-in)

Pour brancher des outils tiers, AJEAN peut exposer `https://<machine>.oai.ajean.link/v1`, authentifié par la clé API du serveur. **Désactivé par défaut**, activable par machine depuis l'interface (panneau *Accès OpenAI*), sans redémarrage.

Le VPS effectue un simple **passthrough SNI** : le TLS est terminé sur la machine hôte (Let's Encrypt via TLS-ALPN-01, à travers le tunnel), le relais ne voit que du chiffré.

## API de pilotage

Le service d'interface expose une API HTTP pour piloter AJEAN à distance. À protéger avant toute exposition :

```bash
ajean set-web-key      # génère une clé
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

> ⚠️ La clé voyage en clair en HTTP. Pour une exposition publique, placer un reverse-proxy HTTPS devant, ou utiliser `ajean link`.

## Compiler depuis les sources

Go 1.25+. AJEAN est écrit à 100 % en Go, l'interface est embarquée via `go:embed` :

```bash
git clone https://github.com/nathaninline/ajean.git
cd ajean

# Linux / Windows :
CGO_ENABLED=0 go build -o ajean ./cmd/ajean

# macOS : le systray passe par Cocoa (CGO obligatoire), donc PAS de CGO_ENABLED=0.
#         Xcode Command Line Tools requis (xcode-select --install).
go build -o ajean ./cmd/ajean
```

> Sur macOS, `CGO_ENABLED=0` exclut les fichiers natifs du systray et échoue sur `undefined: nativeLoop` (issue #30). Compiler sans ce drapeau : CGO est actif par défaut.

> Compiler **AJEAN** ne demande que Go. Compiler le **moteur llama.cpp** demande `git`, `cmake` et le toolkit de l'accélérateur.

## Arborescence

- `cmd/ajean/` : point d'entrée + ressources Windows (icône, versioninfo).
- `internal/ajean/` : tout le code, fichiers préfixés par domaine (`web_*`, `chat_*`, `llm_*`, `backend_*`, `relay_*`, `sys_*`, `mcp_*`) ; carte dans `doc.go`.
- `internal/ajean/ui/` : interface web embarquée. **`index.html` est généré** : les sources vivent dans `ui/src/`. Pour modifier l'interface, éditer `ui/src/` puis lancer `go generate ./internal/ajean`.
- `tools/` : outils hors binaire — `assemble-ui` (génère `index.html`) et `gen-icon` (icônes Windows).

## Migrer depuis la 0.7.x

Une seule commande :

```bash
sudo ajean install
```

`install` détecte l'ancienne disposition et fait la reprise complète : il arrête et **désactive** les anciens services (`jean`, `jean-link`), déplace `configs/` vers `presets/`, `MEMORY/` vers `memory/` et les `.gguf` vers `models/`, reprend en base la configuration, les préférences, la conversation, les clés, le jeton de liaison, les interrupteurs, les serveurs MCP et les benchmarks — puis installe les deux nouvelles unités.

Désactiver les anciennes unités n'est pas un détail : une `jean.service` restée active relancerait un **second** `llama-server` au prochain démarrage, sur le même port et la même VRAM.

Ce qui est garanti :

- **Rien n'est supprimé.** Presets, mémoire et modèles sont *déplacés*, jamais copiés ni effacés. Les petits fichiers d'état sont lus puis rangés dans `avant-0.8/` — supprimez ce dossier quand tout va bien.
- **`.e2e_key` n'est pas touché**, donc l'empreinte de chiffrement confirmée dans le portail reste valable.
- **Une machine déjà migrée à la main** n'est pas écrasée : si `configs/` et `presets/` existent tous les deux, l'ancien est laissé tel quel et signalé.

Après coup : `ajean where`, `ajean switch` (le preset actif doit être détecté) et `ajean test`. Si vous aviez réglé l'unité du moteur à la main (priorité CPU, `CUDA_VISIBLE_DEVICES`, utilisateur dédié), `install` la réécrit : reportez vos directives dans `/etc/systemd/system/ajean-engine.service` puis `systemctl daemon-reload`.

Le code de cette reprise vit dans un fichier unique, `internal/ajean/migrate_07.go`, et sera supprimé d'ici une version ou deux.

## Licence

[MIT](LICENSE). Le `marked.min.js` embarqué est [Marked](https://github.com/markedjs/marked), également MIT.
