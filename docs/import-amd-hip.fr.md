# Importer une installation Strata HIP locale (expérimental)

Pour distinguer Radiance, Paiton et Strata, et le périmètre exact de cette PR, voir [Moteurs AMD](moteurs-amd.fr.md).

Cette contribution ajoute une branche HIP explicite au moteur MoE existant.
Elle conserve le modal, les presets, les tâches d'installation, le chat et ses
quatre modes, la mémoire, le coffre et le compactor AJEAN d'origine. Aucun chat
parallèle, proxy ou serveur d'aperçu n'est ajouté.

**La qualification GPU reste à effectuer.** Les tests CPU vérifient intégrité,
transactions, configuration et remplacement de processus avec des fixtures.
Ils ne valident ni compatibilité AMD réelle, débit, stabilité, MTP/INT8, ni
utilisateurs simultanés. Aucun poids, moteur binaire, environnement Python ou
source tiers n'est livré ; acquérir les composants compatibles séparément en
conservant leurs attributions/licences et les qualifier avant activation.

La cible initiale est Linux x86_64, texte, une génération et écoute loopback.
Le mode d’import HIP local doit être choisi explicitement. La présence d’un
catalogue ne remplace ni la détection matérielle normale ni le parcours CUDA
existant. L’API expose cette vue par `GET /api/moe?backend=hip`. Un import déclaré
ne constitue pas une mesure du matériel.

Créer `AJEAN_HOME/moe-hip/catalogue.json` avec une liste `recipes` décrivant les
chemins absolus et empreintes de la configuration, du moteur, des sources et du
manifest de sources. Le [guide détaillé](amd-hip-import.md) fournit le schéma et
un exemple anonymisé. Toutes ses valeurs de remplacement doivent être remplies.
La configuration Strata déclare explicitement HIP et ses arguments, notamment
`--max-context VALUE` (131072 maximum), avec tokenizer et assets compatibles.
Les empreintes vérifient l'intégrité, pas l'innocuité du code importé. Python,
ses paquets et les bibliothèques système ne sont pas entièrement épinglés par
le seul manifest des sources applicatives.

```sh
AJEAN_HOME=/chemin/du/profil ajean moe-hip-check
```

Cette commande effectue uniquement un audit de fichiers. Dans le modal MoE
original, les segments Swift/Classique et cartes de qualité reprennent les
métadonnées déclarées. Enregistrer/préparer crée un preset ou une variante
inactive sans changer la configuration active ni lancer de modèle. Les réglages
contexte/KV/MTP sont limités aux capacités de la recette ; vision, aide CUDA et
remappage implicite sont refusés. Le bouton d'activation du modal reste désactivé.
L'activation explicite d'un preset par les interfaces de service AJEAN existantes
reste possible et **démarre réellement le moteur** ; ne pas l'utiliser pendant
une validation CPU.

Pour copier une installation locale épinglée, définir un bundle dans
`AJEAN_HOME/moe-hip/install-catalogue.json` puis :

```sh
AJEAN_HOME=/chemin/du/profil ajean moe-hip-install swift-iq3
```

L'installation copie et vérifie les fichiers, puis publie catalogue et preset
après commit de l'installation complète. Elle ne télécharge, ne compile,
n'exécute Python ni n'active le modèle. Le suivi et l'annulation utilisent la
UI d'installation d'origine. Le guide détaillé décrit les schémas `entries`,
manifest schema1 et références locales. La fixture `hipInstallFixture` constitue
un exemple CPU exécutable complet, sans poids ni moteur réels.

Validation : `go test -race ./internal/ajean -run '^TestMoe' -count=1`,
`go run ./tools/verify-i18n`, `go generate ./internal/ajean`, `go build ./cmd/ajean`.
La qualification GPU et les régressions visuelles AMD/NVIDIA sont des étapes
séparées ; les tests CPU ne permettent pas d'annoncer ce backend prêt en production.
