# Brancher un moteur d'IA sur AJEAN

Depuis la version 0.19, AJEAN n'installe plus de moteur. Il ne compile plus llama.cpp ni ExLlama et ne gère plus de presets.

Un **modèle** est désormais un **service système** :

- AJEAN lance une commande qui expose une API compatible OpenAI (une adresse qui se termine par `/v1`).
- AJEAN surveille la RAM et la VRAM pendant le chargement, puis en continu.
- Quand l'API répond, AJEAN branche le chat dessus.

N'importe quel moteur convient, à condition qu'il serve une API OpenAI. Une API distante (OpenAI, OpenRouter, Groq, etc.) convient aussi.

Les anciens presets sont convertis automatiquement en modèles au premier démarrage de la 0.19. Les fichiers de presets d'origine ne sont pas modifiés.

---

## 1. Ajouter un modèle

### Depuis l'interface

1. Ouvrir la section **Presets** du menu : chaque ligne est un modèle.
2. Cliquer sur **+**.
3. Renseigner le **Nom** (l'identifiant en est déduit).
4. Choisir **Où tourne le modèle** :
   - **Sur cette machine** : saisir la **Commande** sur une ligne, comme dans un terminal (chemin complet du serveur du moteur, suivi de ses options). Deux interrupteurs l'accompagnent : **Relancer en cas d'échec** et **Démarrer avec la machine**. Cette ligne reste visible et modifiable dans la fenêtre du modèle.
   - **API distante** : indiquer l'**Adresse de l'API** (base OpenAI, se termine par `/v1`), le **Modèle** (vide = le premier annoncé par l'API) et la **Clé API**.
5. Groupe **Modèle** : taille du **Contexte** (en jetons), interrupteur **Vision** (le modèle accepte les images), bouton **Tester la connexion**.
6. **Prompt système** : facultatif, propre à ce modèle.
7. **Échantillonnage** : température, top-p, top-k, min-p, pénalités de répétition et de présence, effort de réflexion. Un champ vide laisse la valeur du moteur.
8. **Garde-fou** : RAM nécessaire, VRAM nécessaire par carte (ex. `0=15, 1=6`), délai de chargement, option **Un seul à la fois**. Voir la section 3.
9. **Avancé** (pour un modèle local) :
   - **Dossier de travail** ;
   - **Variables d'environnement**, une par ligne au format `NOM=valeur` ;
   - **Utilisateur** (Linux) : compte qui lance la commande. Vide = celui d'AJEAN ;
   - **Adresse de l'API** : déduite de la commande (`--port 8080` donne `http://127.0.0.1:8080/v1`). Elle peut être saisie à la main ;
   - **Modèle** et **Clé API**, si le moteur en exige une. La clé est lue dans la commande quand elle contient `--api-key`.
10. **Enregistrer**, puis **démarrer**.

La fenêtre du modèle affiche aussi son **Journal**, un bouton **Arrêter** et un bouton **Benchmark** pour mesurer la vitesse.

### En ligne de commande

Les définitions sont rangées dans `services.json`, dans le dossier de données d'AJEAN.

| Commande | Effet |
|---|---|
| `ajean service add fichier.json` | ajoute ou remplace un modèle (lecture sur l'entrée standard avec `-`) |
| `ajean service list` | liste les modèles, leur état et leur adresse |
| `ajean service show <id>` | affiche la définition |
| `ajean service start <id> [--force]` | démarre avec le garde-fou (`--force` saute la vérification RAM/VRAM) |
| `ajean service stop <id>` | arrête |
| `ajean service restart <id>` | arrête puis redémarre |
| `ajean service logs <id> [n]` | affiche les `n` dernières lignes du journal (100 par défaut) |
| `ajean service use <id>` | démarre si besoin et branche le chat dessus |
| `ajean service autostart <id> on\|off` | démarrage avec la machine |
| `ajean service why <id>` | raison du dernier arrêt |
| `ajean service rm <id>` | arrête et supprime le modèle |
| `ajean service convert --dry-run` | montre la conversion des anciens presets sans rien écrire (sans `--dry-run` : convertit) |

### Format d'un modèle

`ajean service add` attend un objet JSON. Exemple complet :

```json
{
  "id": "qwen-27b",
  "name": "Qwen 27B",
  "command": "/home/user/llama.cpp/build/bin/llama-server -m /home/user/models/qwen-27b-q4_k_m.gguf -c 32768 -ngl 99 --host 127.0.0.1 --port 8080 --jinja",
  "workdir": "/home/user/llama.cpp",
  "env": {"CUDA_VISIBLE_DEVICES": "0"},
  "url": "http://127.0.0.1:8080/v1",
  "model": "",
  "api_key": "",
  "context": 32768,
  "vision": false,
  "need": {"ram_gb": 4, "vram_gb": {"0": 18}},
  "exclusive": true,
  "ready_timeout_s": 300
}
```

| Champ | Rôle |
|---|---|
| `id` | identifiant : minuscules, chiffres, tirets (41 caractères au plus) |
| `name` | nom affiché (par défaut : l'identifiant) |
| `command` | commande à lancer, sur une ligne ; AJEAN écrit alors lui-même l'unité système |
| `exec`, `args` | ancienne forme (programme, puis un argument par élément), toujours acceptée ; ignorée si `command` est renseigné |
| `workdir` | dossier de travail (doit exister) |
| `env` | variables d'environnement |
| `user` | Linux : compte qui lance la commande (par défaut : celui d'AJEAN) |
| `no_restart` | `true` : ne pas relancer le service après une erreur (Linux) |
| `memory_max_gb` | Linux : plafond de RAM du service, sans swap ; au-delà, le noyau l'arrête |
| `autostart_wanted` | Windows et macOS : démarrer avec la machine. Sous Linux, utiliser `ajean service autostart` |
| `url` | base OpenAI en `http` ou `https`, par exemple `http://127.0.0.1:8080/v1`. Obligatoire pour une API distante ; pour un modèle local, vide = déduite de la commande |
| `model` | nom du modèle à demander ; vide = le premier annoncé |
| `api_key` | clé envoyée en `Authorization: Bearer` |
| `context` | taille du contexte en jetons |
| `vision` | le modèle accepte les images |
| `need` | besoin déclaré : `ram_gb`, et `vram_gb` par index de carte (ordre de `nvidia-smi`) |
| `exclusive` | « Un seul à la fois » |
| `ready_timeout_s` | délai pour que l'API réponde (600 s par défaut) |

Deux cas :

- **`command` renseigné** : AJEAN crée et gère l'unité système du modèle.
- **pas de `command`** : API déjà en marche ou distante. AJEAN vérifie seulement qu'elle répond.

AJEAN ne pilote que les services qu'il a écrits lui-même. Il ne démarre et n'arrête aucun autre service de la machine.

L'adresse d'un modèle local est déduite de la commande dans cet ordre : l'option `--port`, sinon le port écrit dans le fichier passé à `--config`, sinon `8080` pour les moteurs connus.

### Ce qu'AJEAN crée selon le système

| Système | Unité créée | Journal |
|---|---|---|
| Linux | unité systemd système `/etc/systemd/system/ajean-svc-<id>.service` | `journalctl -u ajean-svc-<id>` |
| Windows | service Windows `ajean-svc-<id>` ; AJEAN sert d'enveloppe (`ajean service host <id>`) | fichier `services\<id>.log` du dossier de données |
| macOS | agent launchd `com.ajean.svc.<id>` dans `~/Library/LaunchAgents/` | fichier `services/<id>.log` du dossier de données |

L'unité créée par AJEAN est réécrite depuis `services.json` dès que la définition change. Il faut donc modifier le modèle dans AJEAN, pas l'unité générée.

Sous Linux :

- La commande passe par `bash -lc` : variables, `~` et guillemets s'y comportent comme dans un terminal. Sous Windows et macOS, elle est découpée en arguments, sans shell ; les guillemets regroupent un argument qui contient des espaces.
- Écrire une unité, la démarrer ou l'arrêter demande les droits root. AJEAN passe par un assistant, `/usr/local/sbin/ajean-svc`, installé par `sudo ajean install` (ou `sudo ajean install-helper` seul). L'assistant n'agit que sur les unités des modèles : leur nom est toujours `ajean-svc-<id>.service`, et elles sont listées dans `/etc/ajean-svc/managed.json`. Tout autre nom d'unité est refusé.
- Un modèle supprimé voit son unité arrêtée, désactivée et mise de côté dans `/etc/ajean-svc/supprimes/`.

Relance automatique en cas de plantage :

- Linux : `Restart=on-failure` après 5 secondes, 3 démarrages au plus en 5 minutes. L'interrupteur **Relancer en cas d'échec** (`no_restart`) la coupe.
- Windows : l'enveloppe relance le programme après 5 secondes, 3 fois au plus en 5 minutes.
- macOS : `KeepAlive` si le programme se termine en erreur.

---

## 2. Guide par moteur

Chaque exemple donne la **Commande** à saisir. L'adresse de l'API correspond au port choisi : `--port 8080` donne `http://127.0.0.1:8080/v1`.

### llama.cpp (llama-server)

**Installation** : suivre le dépôt officiel https://github.com/ggml-org/llama.cpp. Les versions précompilées sont sur la page des releases du dépôt. La compilation est décrite dans `docs/build.md` du dépôt. Le programme utile est `llama-server`.

**Modèle AJEAN** (Sur cette machine), champ **Commande** :

```
/home/user/llama.cpp/build/bin/llama-server -m /home/user/models/modele.gguf -c 32768 -ngl 99 --host 127.0.0.1 --port 8080 --jinja
```

- `-m` : fichier GGUF du modèle.
- `-c` : taille du contexte.
- `-ngl` : nombre de couches placées sur le GPU (`99` = tout).
- `--host 127.0.0.1` : API accessible depuis cette machine seulement.
- `--jinja` : utilise le modèle de conversation du GGUF (nécessaire pour les outils).
- **Vision** : ajouter `--mmproj` puis le chemin du fichier mmproj, et cocher **Vision**.
- **Clé facultative** : ajouter `--api-key` puis une clé (ex. `sk-exemple`). AJEAN la lit dans la commande.

Sous Windows, la commande commence par un chemin comme `C:\llama.cpp\llama-server.exe`. Un chemin qui contient des espaces se met entre guillemets.

### ExLlama via TabbyAPI

**Installation** : suivre le dépôt officiel https://github.com/theroyallab/tabbyAPI (le moteur ExLlama est https://github.com/turboderp-org/exllamav3). TabbyAPI se règle par un fichier `config.yml` : dossier des modèles, modèle chargé, hôte et port.

**Modèle AJEAN**, champ **Commande** (l'interpréteur Python de l'environnement de TabbyAPI, puis son script) :

```
/home/user/tabbyAPI/venv/bin/python main.py --config /home/user/tabbyAPI/config.yml
```

Avancé :
- **Dossier de travail** : `/home/user/tabbyAPI`.
- **Adresse de l'API** : AJEAN lit le port dans `config.yml`. S'il ne peut pas lire ce fichier, la saisir à la main. Exemple : `http://127.0.0.1:5000/v1`.
- **Clé API** : TabbyAPI génère des clés dans son dossier (fichier `api_tokens.yml`). Reporter la clé d'API dans ce champ, ou désactiver l'authentification dans `config.yml`.

### vLLM

**Installation** : suivre la documentation officielle https://docs.vllm.ai et le dépôt https://github.com/vllm-project/vllm.

**Modèle AJEAN**, champ **Commande** (la commande `vllm` de l'environnement Python) :

```
/home/user/vllm/venv/bin/vllm serve /home/user/models/mon-modele --host 127.0.0.1 --port 8000
```

L'adresse déduite est `http://127.0.0.1:8000/v1`. vLLM réserve par défaut la plus grande partie de la VRAM : déclarer ce besoin dans **VRAM nécessaire** évite les surprises. Le chargement peut être long : augmenter le **Délai de chargement** si besoin.

### Tout autre moteur

Les autres moteurs se branchent comme n'importe quel programme qui sert une API OpenAI :

1. Installer le moteur selon sa propre documentation.
2. Vérifier à la main qu'il démarre et que `http://127.0.0.1:<port>/v1/models` répond.
3. Dans AJEAN : **Commande** = celle qui vient d'être testée, sur une ligne.
4. Si le port n'est pas passé par `--port`, saisir l'**Adresse de l'API** dans **Avancé**.
5. Renseigner **Dossier de travail** et **Variables d'environnement** si le moteur en a besoin.

### Un moteur déjà lancé par un service système

AJEAN ne reprend pas un service existant : il ne pilote que ceux qu'il écrit. Pour un moteur déjà lancé par une unité systemd, un service Windows ou un agent launchd :

1. Relever sa ligne de commande, son dossier de travail et ses variables d'environnement. Sous Linux : `systemctl cat mon-moteur.service`, lignes `ExecStart`, `WorkingDirectory` et `Environment`.
2. Créer le modèle dans AJEAN avec cette **Commande** (et, dans **Avancé**, le dossier de travail et les variables).
3. Arrêter et désactiver l'ancien service, pour libérer le port. Sous Linux : `sudo systemctl disable --now mon-moteur.service`.
4. Démarrer le modèle depuis AJEAN.

### API distante (OpenAI, OpenRouter, Groq…)

Dans l'interface, choisir **API distante**. En JSON, ne pas mettre `command` :

```json
{
  "id": "openrouter",
  "name": "OpenRouter",
  "url": "https://openrouter.ai/api/v1",
  "model": "nom/du-modele",
  "api_key": "sk-exemple",
  "context": 128000
}
```

Adresses usuelles :

| Fournisseur | Adresse de l'API |
|---|---|
| OpenAI | `https://api.openai.com/v1` |
| OpenRouter | `https://openrouter.ai/api/v1` |
| Groq | `https://api.groq.com/openai/v1` |

AJEAN vérifie que `<adresse>/models` répond avec la clé fournie. Aucune surveillance mémoire ne s'applique.

---

## 3. Le garde-fou

### Avant le démarrage

AJEAN refuse de démarrer si :

- le **dossier de travail** n'existe pas ;
- le **port** de l'adresse locale est déjà utilisé par un programme qu'AJEAN ne gère pas ;
- la **RAM** ou la **VRAM** libres ne suffisent pas.

Si le port est tenu par un autre modèle, AJEAN propose d'arrêter celui-ci et de démarrer le nouveau. Si le nouveau échoue aussitôt, l'ancien est remis en service.

Le besoin retenu est le plus grand de deux valeurs :

- le **besoin déclaré** (`need`, champs RAM et VRAM nécessaires) ;
- le **pic mesuré** au dernier lancement réussi, affiché dans l'interface (« pic mesuré »).

Pour la RAM, une marge égale au seuil de RAM libre (voir plus bas) est gardée. Pour la VRAM, une marge de 256 Mo est gardée par carte.

En cas de refus, le message liste les services en marche à arrêter pour faire de la place. `--force` (ou « Démarrer quand même » dans l'interface) passe outre cette vérification mémoire. Le gardien RAM reste actif.

### Pendant le chargement

Chaque seconde, AJEAN relève la RAM et la VRAM. Il arrête le service si :

- la **RAM libre** passe sous le **seuil** ;
- le service s'arrête de lui-même. Dans les premières secondes, l'erreur donne la cause lue dans le journal (port déjà pris, mémoire insuffisante, fichier introuvable, droits, option inconnue) ; ensuite, ses 20 dernières lignes. Il n'est pas relancé ;
- sous Linux, le gardien RAM vient de l'arrêter : il n'est pas relancé non plus ;
- l'API ne répond pas dans le **délai de chargement** (600 s par défaut, champ `ready_timeout_s`).

Le service est prêt quand `<adresse>/models` répond 200. Les pics de RAM et de VRAM sont alors mémorisés pour affiner la vérification suivante.

### En marche

**Sous Linux**, un gardien root tourne en permanence : le service `ajean-guard`, installé par `sudo ajean install`. Si la mémoire disponible passe sous son seuil, il arrête d'un coup le modèle le plus gourmand. Si aucun modèle ne tourne, il arrête le plus gros service non protégé de la machine. Il agit avant que la machine ne se fige, même si l'interface d'AJEAN est arrêtée. Avec **Relancer en cas d'échec**, le système relance ensuite le modèle, dans la limite des plantages répétés.

- **Seuil du gardien** : 2048 Mio. La variable `AJEAN_GUARD_MIB` le remplace ; elle se pose par un fichier d'ajout systemd (drop-in) sur `ajean-guard`, car l'unité est réécrite à chaque installation.
- Ses interventions sont consignées dans `/etc/ajean-svc/guard.json` et dans `journalctl -u ajean-guard`. Quand un modèle reste arrêté après l'une d'elles, l'interface et `ajean service why <id>` l'indiquent.

**Sous Windows et macOS** (ou sous Linux sans le gardien), le serveur web d'AJEAN garde un chien de garde. Toutes les 2 secondes, si la RAM libre passe sous le seuil, il arrête le modèle local démarré le plus récemment.

**Seuil de RAM libre** (vérification avant démarrage, chargement, chien de garde du serveur web) : 3 % de la RAM totale, avec un minimum de 1 Go. La variable d'environnement `AJEAN_RAM_FLOOR_MB` le remplace (valeur en Mo).

### Plantages répétés

Un service qui plante est relancé par le système. Après 3 plantages en 5 minutes, il reste arrêté et apparaît en **erreur**.

### Un seul à la fois

Plusieurs modèles peuvent tourner en même temps. L'option **Un seul à la fois** (`exclusive`) change ce comportement : démarrer un modèle marqué ainsi arrête les autres modèles marqués ainsi. Utile avec une seule carte graphique.

---

## 4. Dépannage

**« le port … est utilisé »**
Un autre programme écoute déjà sur ce port ; le message le nomme quand il est connu. L'arrêter, ou choisir un autre `--port`. Après une mise à jour depuis la 0.18, il s'agit souvent de l'ancien moteur, qu'AJEAN n'arrête plus : sous Linux, `sudo systemctl disable --now ajean-engine`.

**« mémoire insuffisante »**
Arrêter les services cités dans le message, réduire le contexte ou le nombre de couches sur le GPU, ou corriger le besoin déclaré s'il est trop élevé.

**Le service s'arrête pendant le chargement**
Lire le journal : bouton **Journal** dans la fenêtre du modèle, ou `ajean service logs <id>`. Les causes fréquentes sont un chemin de modèle faux, une option inconnue du moteur ou un manque de VRAM. Tester ensuite la même commande à la main dans un terminal.

**Pas de réponse dans le délai**
Les gros modèles peuvent charger lentement. Augmenter le **Délai de chargement**. Vérifier aussi que l'adresse de l'API correspond bien au port du moteur.

**Le modèle s'arrête sans message**
`ajean service why <id>` donne la raison du dernier arrêt, y compris une intervention du gardien RAM sous Linux (détail dans `journalctl -u ajean-guard`).

**Linux : « l'assistant root d'AJEAN n'est pas installé »**
Exécuter `sudo ajean install-helper`. La commande est à relancer après chaque mise à jour d'AJEAN : `ajean update` ne remplace pas l'assistant root ni le gardien RAM, qui restent sinon à l'ancienne version.

**Windows : invite d'administrateur**
La création du service Windows demande les droits d'administrateur une seule fois (invite UAC). Ensuite, le démarrage et l'arrêt se font sans droits particuliers. Si l'invite est refusée, le service n'est pas créé : relancer le démarrage et accepter l'invite.
