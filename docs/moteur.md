# Le moteur llama.cpp d'AJEAN

Depuis la 0.20, AJEAN gère son moteur comme Ollama gère le sien : **une version
d'AJEAN = une version précise de llama.cpp**, choisie et testée par le
mainteneur. L'utilisateur ne choisit pas de moteur, ne le compile pas et ne le
met pas à jour. Ce document explique le fonctionnement et, surtout, comment
changer de version de llama.cpp sans casser les installations.

## Ce qui a été retiré, et pourquoi

Jusqu'à la 0.19, AJEAN proposait cinq façons d'obtenir un moteur : précompilé
(la *dernière* release de llama.cpp, quelle qu'elle soit), compilé localement,
forks personnalisés, ExLlamaV3 et AJEAN MoE. Chaque machine faisait donc
tourner une combinaison différente, impossible à tester avant une release, et
chaque nouveauté amont pouvait casser AJEAN chez les utilisateurs sans qu'aucune
ligne d'AJEAN n'ait changé.

La 0.20 ne garde qu'un chemin : le `llama-server` officiel, à une version
épinglée.

| Retiré | Remplacé par |
|---|---|
| `ajean llamacpp install / update / uninstall`, compilation locale | installation automatique du moteur épinglé |
| « dernière release » de llama.cpp | le tag de `LLAMA_CPP_VERSION` |
| moteurs personnalisés (forks), clé `BIN` | rien : un seul moteur |
| ExLlamaV3 (`ENGINE=exl3`), AJEAN MoE (`ENGINE=moe`), GPU Cloud (`CLOUD=modal`) | pour un autre moteur : un preset **Service** (`backend_service.go`), qui lance la commande de votre choix dans le service moteur |

Un preset `ENGINE=exl3`, `ENGINE=moe` ou `CLOUD=modal` reste listé (grisé) mais refuse de
démarrer, avec un message clair. Une ligne `BIN=` dans un ancien preset est
ignorée. Les dossiers laissés par les anciennes versions (`backends/llama.cpp`,
moteurs personnalisés, `exl3/`) ne sont pas supprimés : `ajean engine` et la
section **Moteur** les signalent. Seul l'ancien `backends/llama.cpp-prebuilt`,
entièrement géré par AJEAN, est nettoyé.

## Comment ça marche

### 1. Une version épinglée dans le dépôt

- `internal/ajean/LLAMA_CPP_VERSION` : le tag amont (ex. `b12257`).
- `internal/ajean/llama_cpp_assets.json` : les archives de ce tag que sait
  installer AJEAN, avec leur taille et leur **empreinte SHA-256**.

Les deux fichiers sont embarqués dans le binaire (`go:embed`). Une machine
installe donc exactement les octets qui ont été validés : une archive dont
l'empreinte diffère n'est jamais installée.

### 2. Installation automatique

Le moteur s'installe dans `backends/engine/<tag>/` :

- au démarrage de l'interface (`ajean web`, application de bureau) s'il manque,
  en tâche de fond, avec la progression dans la section **Moteur** ;
- au démarrage du service (`ajean serve`) s'il manque encore, avant de charger
  le modèle.

Après une mise à jour d'AJEAN qui change de tag, le moteur de l'ancien tag n'est
plus « celui de cette version » : le nouveau est installé au démarrage suivant,
puis l'ancien est supprimé. Les bibliothèques CUDA (≈ 400 Mo), qui changent
rarement d'une version à l'autre, sont recopiées plutôt que retéléchargées
quand leur empreinte est identique.

Un fichier verrou (`backends/engine/.lock`) évite que le service et l'interface
installent en même temps. Les téléchargements reprennent où ils se sont arrêtés.

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
et la suivante est essayée. Le moteur n'est mis en place (renommage du dossier)
qu'une fois la sonde passée : une installation ratée ne remplace jamais un
moteur qui marche.

Les bibliothèques CUDA sont livrées avec le moteur (`runtime/`) : seul le pilote
de la carte est requis sur la machine.

Pour une carte que la détection sert mal :

```
ajean engine repair --variant=vulkan     # ou cuda, rocm, cpu ; auto pour revenir
```

### 4. Erreurs lisibles

Quand le moteur s'arrête, AJEAN cherche dans son journal les lignes d'erreur
connues (`error loading model`, `cudaMalloc failed`, `out of memory`, `unknown
argument`…) et les remonte à l'interface, plutôt qu'un code de sortie
(`engine_status.go`).

### 5. Pour le développement

`AJEAN_ENGINE_BIN=/chemin/vers/llama-server` remplace le moteur livré. C'est le
moyen d'essayer une version candidate de llama.cpp avant de l'épingler. Rien
dans l'interface ne le propose.

## Changer de version de llama.cpp

À faire régulièrement et par petits pas (Ollama le fait à peu près chaque
semaine) : plus l'écart est grand, plus il y a de changements à relire.

1. **Épingler le nouveau tag** (une release `bNNNNN` de ggml-org/llama.cpp
   dont tous les fichiers sont publiés) :

   ```
   go run ./tools/bump-llamacpp b12345
   ```

   La commande réécrit `LLAMA_CPP_VERSION` et `llama_cpp_assets.json` à partir
   de l'API GitHub.

2. **Vérifier que chaque machine a encore ses archives** :

   ```
   go test ./internal/ajean -run Engine
   ```

   `TestEngineManifestCouvreChaqueMachine` échoue si llama.cpp a renommé ou
   retiré une archive (Windows/Linux/macOS, CUDA/ROCm/Vulkan/CPU).

3. **Relire ce dont AJEAN dépend** dans le diff amont entre les deux tags :
   - les drapeaux passés par `ajean serve` (`backend_serve.go`) : `-m -ngl -c
     -t -tb -b -ub -np --host --port -ctk -ctv --mmproj --model-draft
     --reasoning --reasoning-budget --api-key --load-mode`, et ceux que
     l'éditeur de preset écrit dans `EXTRA_ARGS` ;
   - la sortie de `--list-devices` (`parseListDevices`, `web_devices.go`) ;
   - `/health`, `/v1/chat/completions` et les `timings` (`llm_client.go`,
     `llm_bench.go`) ;
   - les lignes de journal lues par AJEAN (`modelLoadError` dans `web_api.go`,
     `engine_status.go`).

4. **Essayer pour de vrai**, sur au moins une machine par variante disponible :
   `ajean engine repair`, charger un modèle, envoyer une requête, une image
   (vision), un appel d'outil, un bench. Charger le modèle ne suffit pas.

5. Mentionner le nouveau tag dans `RELEASE_NOTES.md`.

## Fichiers

| Fichier | Rôle |
|---|---|
| `internal/ajean/LLAMA_CPP_VERSION`, `llama_cpp_assets.json` | la version épinglée |
| `tools/bump-llamacpp` | change la version épinglée |
| `engine.go` | version, emplacements, choix de la variante, restes des anciennes versions |
| `engine_install.go` | verrou, téléchargement, sonde, mise en place, `ajean engine` |
| `engine_archive.go` | téléchargement vérifié avec reprise, extraction |
| `engine_detect.go` | carte NVIDIA, version CUDA du pilote, ROCm, Vulkan |
| `engine_status.go` | erreurs du journal du moteur |
| `web_engine.go`, `web_engine_job.go` | API `/api/engine*`, suivi de l'installation |
| `ui/src/js/10-engine.js` | la carte **Moteur** de l'interface |
