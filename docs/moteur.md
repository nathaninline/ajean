# Le moteur AJEAN (llama.cpp épinglé)

AJEAN sait faire tourner plusieurs moteurs. La 0.21 en ajoute un, le **moteur
AJEAN** : le `llama-server` officiel de llama.cpp, à **une version précise**,
choisie et testée par le mainteneur pour chaque version d'AJEAN (comme Ollama
gère le sien). Il s'ajoute aux autres, il ne les remplace pas.

| Moteur | D'où il vient | Qui le met à jour |
|---|---|---|
| **Moteur AJEAN** | `backends/engine/<tag>/`, tag de `LLAMA_CPP_VERSION` | AJEAN, à chaque version |
| llama.cpp compilé | `ajean llamacpp install` (`backends/llama.cpp`) | l'utilisateur |
| llama.cpp précompilé | dernière release amont (`backends/llama.cpp-prebuilt`) | l'utilisateur |
| forks personnalisés | `backends/<nom>` | l'utilisateur |
| ExLlama, AJEAN MoE, GPU cloud | `ENGINE=exl3`, `ENGINE=moe`, `CLOUD=modal` | comme en 0.18 |
| Service | une commande au choix (`SERVICE_CMD`, `backend_service.go`) | l'utilisateur |

La 0.20 avait remplacé tous les moteurs par le moteur épinglé. La 0.21 revient
sur ce choix : rien de ce que fait la 0.18 n'est retiré, aucun dossier de moteur
n'est nettoyé ni signalé comme un reste.

## Quel moteur lance un preset

La clé `BIN` du preset le dit, et une seule fonction la lit : `resolveBin`
(`engine_resolve.go`), utilisée par le service, le pré-vol, les drapeaux de
chargement, la vidéo, les embeddings et la liste des cartes graphiques.

| `BIN` | Moteur lancé |
|---|---|
| un chemin hors de `backends/engine/` (compilé, précompilé, fork, chemin au choix) | ce binaire, comme en 0.18 ; le précompilé suit sa release courante |
| un chemin dans `backends/engine/<tag>/` | le moteur AJEAN de la version **courante**, même si le chemin est celui d'un ancien tag |
| vide | le moteur AJEAN, installé par le service s'il manque |

Les presets `ENGINE=exl3`, `ENGINE=moe`, `CLOUD=modal`, API et Service ne lisent
pas `BIN` : ils ont leur propre lancement.

## Comment ça marche

### 1. Une version épinglée dans le dépôt

- `internal/ajean/LLAMA_CPP_VERSION` : le tag amont (ex. `b12257`).
- `internal/ajean/llama_cpp_assets.json` : les archives de ce tag que sait
  installer AJEAN, avec leur taille et leur **empreinte SHA-256**.

Les deux fichiers sont embarqués dans le binaire (`go:embed`). Une archive dont
l'empreinte diffère n'est jamais installée.

### 2. Installation

Le moteur s'installe dans `backends/engine/<tag>/` :

- à la demande : `ajean engine install`, ou `POST /api/engine/install` ;
- au démarrage du service (`ajean serve`) si le preset actif tourne dessus et
  qu'il manque (`engineEnsure`) ;
- au démarrage de l'interface, en tâche de fond, **seulement** si la
  configuration active tourne dessus ou si aucun moteur n'est installé sur la
  machine (`engineWanted`). Chez quelqu'un qui a déjà son moteur, rien n'est
  téléchargé.

Après une mise à jour d'AJEAN qui change de tag, le nouveau moteur est installé
au démarrage suivant d'un preset qui l'utilise, puis l'ancien tag est supprimé
de `backends/engine/` (et seulement de là). Les bibliothèques CUDA (≈ 400 Mo)
sont recopiées plutôt que retéléchargées quand leur empreinte est identique.

Un fichier verrou (`backends/engine/.lock`) évite que le service et l'interface
installent en même temps. Les téléchargements reprennent où ils se sont arrêtés.
L'installation depuis l'interface est un job comme la compilation : même suivi
(`/api/llamacpp/job`), un seul job de moteur à la fois.

### 3. Choix de la variante, avec sonde

| Système | Ordre d'essai |
|---|---|
| Windows x64 | CUDA (si carte NVIDIA, version ≤ celle du pilote) → Vulkan → CPU |
| Windows arm64 | CPU |
| macOS | Metal |
| Linux | ROCm (si installé) → CUDA (si carte NVIDIA) → Vulkan → CPU |

Chaque variante est extraite dans un dossier provisoire puis **sondée dans un
processus jetable** (`llama-server --list-devices`, 30 s, 90 s sous Windows).
Une variante GPU qui plante, ne répond pas ou ne voit aucune carte est écartée
et la suivante est essayée. Le moteur n'est mis en place qu'une fois la sonde
passée : une installation ratée ne remplace jamais un moteur qui marche.

Les bibliothèques CUDA sont livrées avec le moteur (`runtime/`) et passent avant
celles d'un CUDA Toolkit de la machine : seul le pilote de la carte est requis.

```
ajean engine                           # version livrée, variante installée, usage
ajean engine install                   # l'installer
ajean engine repair                    # le retélécharger
ajean engine repair --variant=vulkan   # ou cuda, rocm, cpu ; auto pour revenir
```

### 4. Erreurs lisibles

Quand un preset qui tourne sur le moteur AJEAN s'arrête, AJEAN cherche dans le
journal les lignes d'erreur connues (`error loading model`, `cudaMalloc failed`,
`out of memory`, `unknown argument`…) et les remonte à l'interface
(`engine_status.go`). Les autres moteurs gardent les messages de la 0.18.

### 5. Pour le développement

`AJEAN_ENGINE_BIN=/chemin/vers/llama-server` remplace le moteur AJEAN. C'est le
moyen d'essayer une version candidate de llama.cpp avant de l'épingler.

## Venir d'une autre version

- **0.18** : rien ne change. Les presets gardent leur `BIN`, le moteur AJEAN
  n'est pas installé tant qu'on ne le demande pas.
- **0.19** (`migrate_019.go`) : les services deviennent des presets Service ou
  API ; les presets de la 0.18 repartent tels quels.
- **0.20** (`migrate_020.go`) : les presets Local restent sur le moteur AJEAN.
  Une ligne `BIN` héritée de la 0.18, que la 0.20 ignorait, est mise en
  commentaire dans le preset. Le moteur déjà installé est réutilisé si le tag
  est le même. Le mode agent et l'accès internet restent actifs.

## Changer de version de llama.cpp

À faire régulièrement et par petits pas : plus l'écart est grand, plus il y a de
changements à relire.

1. **Épingler le nouveau tag** (une release `bNNNNN` de ggml-org/llama.cpp
   dont tous les fichiers sont publiés) :

   ```
   go run ./tools/bump-llamacpp b12345
   ```

2. **Vérifier que chaque machine a encore ses archives** :

   ```
   go test ./internal/ajean -run Engine
   ```

   `TestEngineManifestCouvreChaqueMachine` échoue si llama.cpp a renommé ou
   retiré une archive.

3. **Relire ce dont AJEAN dépend** dans le diff amont entre les deux tags :
   - les drapeaux passés par `ajean serve` (`backend_serve.go`) et ceux que
     l'éditeur de preset écrit dans `EXTRA_ARGS` ;
   - la sortie de `--list-devices` (`parseListDevices`, `web_devices.go`) ;
   - `/health`, `/v1/chat/completions` et les `timings` (`llm_client.go`,
     `llm_bench.go`) ;
   - les lignes de journal lues par AJEAN (`modelLoadError` dans `web_api.go`,
     `engine_status.go`).

4. **Essayer pour de vrai**, sur au moins une machine par variante disponible :
   `ajean engine repair`, charger un modèle, envoyer une requête, une image, un
   appel d'outil, un bench.

5. Mentionner le nouveau tag dans `RELEASE_NOTES.md`.

## Fichiers

| Fichier | Rôle |
|---|---|
| `internal/ajean/LLAMA_CPP_VERSION`, `llama_cpp_assets.json` | la version épinglée |
| `tools/bump-llamacpp` | change la version épinglée |
| `engine.go` | version, emplacements, choix de la variante |
| `engine_resolve.go` | quel binaire lance un preset ; quand installer sans qu'on le demande |
| `engine_install.go` | verrou, téléchargement, sonde, mise en place, `ajean engine` |
| `engine_archive.go` | téléchargement vérifié avec reprise |
| `engine_detect.go` | carte NVIDIA, version CUDA du pilote, ROCm, Vulkan |
| `engine_status.go` | erreurs du journal du moteur |
| `web_engine.go` | API `/api/engine`, `/api/engine/install`, `/api/engine/repair` |
| `migrate_019.go`, `migrate_020.go` | reprise des installations 0.19 et 0.20 |

## Dans l'interface

- **Section Moteur** (panneau latéral, `#/engine`) : la carte « Moteur AJEAN (llama.cpp bNNNNN) »
  s'affiche en tête, avant les trois moteurs de la 0.18. Un clic l'installe ; « Réparer » le
  retélécharge ; quand plusieurs variantes existent pour la machine (CUDA, Vulkan…), une liste
  permet d'en imposer une. Le suivi passe par le journal d'installation habituel.
- **Éditeur de preset, Moteur** : « Moteur AJEAN » est choisi quand le preset n'a pas de ligne
  `BIN` (ou qu'elle pointe dans `backends/engine/`). Le choisir retire la ligne `BIN` ; les
  presets 0.18 qui désignent un précompilé, un compilé ou un fork s'affichent comme avant.
- **Éditeur de preset, Exécution > Service** : une commande (`SERVICE_CMD`) lancée par AJEAN dans
  son service, qui expose une API compatible OpenAI. L'adresse et la clé se déduisent de la
  commande ; quitter Service retire `SERVICE_CMD`, `SERVICE_WORKDIR` et `SERVICE_ENV`.
- **Configuration** : la ligne Moteur affiche « Moteur AJEAN (llama.cpp bNNNNN) » quand la
  configuration active tourne dessus.
