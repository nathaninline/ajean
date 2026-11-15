<p align="center"><img src="docs/banner.svg" alt="AJEAN" width="100%"></p>

# AJEAN

**Un assistant personnel qui se souvient de vous.** Un seul fichier exécutable : on le branche sur une API compatible OpenAI et c'est parti. Ni Docker, ni Python, ni Node à installer.

AJEAN reprend l'idée de l'assistant personnel du projet [AJEAN](https://github.com/nathaninline/ajean) d'origine et la refait au propre, pour en faire un programme à part entière.

## Démarrer

Téléchargez le fichier de votre système sur [ajean.app](https://ajean.app) ou dans les [releases](https://github.com/nathaninline/ajean/releases) (Windows, macOS, Linux), puis lancez-le. Ou compilez-le vous-même :

```bash
go build -o ajean.exe ./cmd/ajean
```

```bash
./ajean.exe
```

L'interface s'ouvre sur `http://127.0.0.1:8090`. Au premier lancement, la fenêtre **Réglages** demande de brancher une API : fournisseur (OpenAI, Anthropic, OpenRouter, Mistral, Groq, DeepSeek, Gemini, Ollama, LM Studio ou toute URL compatible), clé et modèle. Le bouton *Tester* vérifie la connexion avant d'enregistrer.

### Plusieurs API

- Un **fournisseur** est une API : un nom, une URL, une clé. On en ajoute autant qu'on veut.
- Un **modèle** est ce que l'on choisit pour discuter : un nom (« Rapide », « Réfléchi »…), un fournisseur, l'identifiant du modèle chez lui et un effort de réflexion. On en change d'un clic, sous la zone de saisie.
- La **dictée** peut passer par une API de transcription (fournisseur et modèle au choix) ; sans elle, c'est celle du navigateur.
- L'**interface** existe en français, en anglais et en vietnamien (réglage Langue). AJEAN, lui, répond dans la langue de vos messages.

## Ce qu'il sait faire

- **Se souvenir.** Un profil court (qui vous êtes), des *fiches* (procédures, recettes, listes gardées en entier), des *leçons* (ce qu'il a appris de vos corrections), des *règles d'écriture* appliquées mécaniquement, et un journal de tous les échanges. La mémoire est commune à toutes les conversations.
- **Se rappeler tout seul.** Avant chaque réponse, ce que la mémoire contient de pertinent pour votre message lui est mis sous les yeux : il n'a pas à penser à chercher.
- **Apprendre après coup.** Quand vous ne parlez plus, une *révision* silencieuse relit l'échange et retient ce qui mérite de l'être (un fait, une correction, une procédure).
- **Dormir.** La nuit, un *sommeil* relit toute la mémoire et la range : doublons fusionnés, contradictions tranchées, fiches resserrées. Une copie est prise avant chaque sommeil (restaurable depuis l'interface) et un garde-fou remet en place tout ce que le ménage aurait perdu.
- **Plusieurs conversations**, chacune enregistrée en Markdown lisible.
- **Des tâches** : tout ce qu'il doit faire plus tard ou régulièrement. Un rappel (« rappelle-moi demain à 9 h d'appeler le garage »), une veille (« chaque matin, regarde le prix du vol et préviens-moi s'il passe sous 300 € »), un travail récurrent. La tâche tourne seule, à l'écart de vos conversations, et son compte rendu arrive comme un message de sa part, avec une notification. L'écran **Tâches** permet de les créer, suspendre, lancer et relire.
- **Des applications** : « crée-moi une app pour suivre mes dépenses ». Il écrit une petite application que vous ouvrez depuis l'écran **Applications**, et qu'il améliore à la demande. Chaque version est gardée : on peut revenir en arrière.
- **Travailler sur des fichiers**, uniquement dans son dossier `WORKSPACE` : il ne peut pas en sortir.
- **Piloter un navigateur** (Chrome, Chromium, Edge ou Brave) : ouvrir une page, la lire, cliquer, saisir.
- **Se fabriquer des outils** : des scripts qu'il écrit une fois, teste et réutilise, rangés dans `TOOLS`.
- **Pièces jointes et dictée** : déposez des fichiers (les images sont montrées au modèle s'il les accepte), dictez au micro.

## Où sont les données

Tout vit à côté de l'exécutable :

| Dossier | Contenu |
|---|---|
| `MEMORY/` | toute la mémoire, en Markdown : `profile.md`, `lessons.md`, `rules.md`, `fiches/`, `journal/`, `conversations/`. Lisible et modifiable à la main. |
| `WORKSPACE/` | le seul dossier où l'assistant lit et écrit des fichiers. Vos pièces jointes arrivent dans `uploads/`, ses téléchargements dans `downloads/`. |
| `TOOLS/` | les outils qu'il s'est fabriqués, un script `.js` par outil. |
| `APPS/` | les applications qu'il a créées, avec leurs versions et leurs données. |
| `config.json` | les fournisseurs (URL, clés), les modèles et les préférences. |
| `.ajean/` | l'état technique : compteurs, tâches planifiées, copies d'avant sommeil, profil du navigateur piloté, moteur local d'embeddings et son modèle (`embedder/`, retéléchargeables). |

Sauvegarder AJEAN, c'est copier ce dossier.

## Ce qui l'enferme

- **Fichiers** : tous les accès passent par une racine verrouillée par le système (`os.Root`) sur `WORKSPACE`. Un chemin en `..`, un chemin absolu ou un lien symbolique vers l'extérieur sont refusés.
- **Pas de commandes système** : aucun outil ne lance de programme sur la machine. Une commande ne pourrait pas être enfermée.
- **Ses outils** tournent dans un interpréteur JavaScript intégré au binaire. Un script n'a accès qu'à ce qu'on lui donne : les fichiers du `WORKSPACE` et le web. Rien d'autre n'existe pour lui.
- **Réseau** : ni ses outils ni son navigateur ne peuvent joindre la machine elle-même (dont sa propre interface) ni, par défaut, le réseau local.
- **Navigateur** : profil dédié (jamais le vôtre), adresses `http(s)` uniquement, téléchargements dans `WORKSPACE/downloads`.
- **Applications** : une app s'affiche dans un cadre sans origine et sans réseau. Elle ne peut joindre ni Internet ni AJEAN ; elle n'a que ses propres données, les outils que l'assistant lui a explicitement ouverts, et une question au modèle.

## Les outils qu'il se fabrique

Un outil est un fichier `TOOLS/<nom>.js` qui définit `function run(args)`. Ils ne sont pas envoyés au modèle à chaque message : un seul outil, `tools`, est toujours présent, et c'est par lui qu'il les liste, les charge et les lance à la demande. Mille outils ne coûtent rien au contexte.

```js
/* TOOL {"description":"Convertit une somme en euros vers une autre devise","params":{"montant":"Somme en euros","devise":"Code de la devise, ex. USD"}} */
function run(args) {
  var r = http.get("https://api.frankfurter.app/latest?from=EUR&to=" + args.devise);
  var taux = JSON.parse(r.body).rates[args.devise];
  return args.montant + " EUR = " + (args.montant * taux).toFixed(2) + " " + args.devise;
}
```

Disponible dans un script : `args`, `ws.read / write / list / exists / remove / move`, `http.get / post / request`, `console.log`, et le JavaScript standard (`JSON`, `Math`, `Date`…).

## Réglages avancés

Variables d'environnement, toutes facultatives :

| Variable | Rôle | Défaut |
|---|---|---|
| `AJEAN_HOME` | dossier des données | celui de l'exécutable |
| `AJEAN_HOST`, `AJEAN_PORT` | adresse d'écoute | `127.0.0.1:8090` |
| `AJEAN_WEB_KEY` | clé exigée par l'interface (obligatoire hors de `127.0.0.1`) | aucune |
| `AJEAN_API_URL`, `AJEAN_API_KEY`, `AJEAN_MODEL` | l'API, à la place des réglages | |
| `AJEAN_NO_BROWSER` | ne pas ouvrir l'interface au démarrage | |
| `AJEAN_BROWSER` | navigateur que l'assistant pilote (chemin complet) | détecté |
| `AJEAN_BROWSER_HEADLESS` | le piloter sans fenêtre | |
| `AJEAN_TOOLS_LAN` | autoriser ses outils et son navigateur à joindre le réseau local | |

**Recherche par le sens.** Avec elle, la recherche dans le journal et le rappel automatique retrouvent un souvenir même formulé autrement. Sans elle, la recherche se fait par mots. Deux façons de l'activer dans les réglages :

- **le moteur local** : AJEAN télécharge lui-même [llama.cpp](https://github.com/ggml-org/llama.cpp) (version épinglée, empreinte vérifiée) et un petit modèle d'embeddings depuis Hugging Face (par défaut EmbeddingGemma, 330 Mo), puis le fait tourner sur le processeur. Rien ne sort de la machine. Le moteur démarre à la première recherche et s'arrête après vingt minutes sans usage. Un autre modèle GGUF de Hugging Face peut être indiqué par son adresse ;
- **une API** : un fournisseur et un *modèle d'embeddings* (par exemple `text-embedding-3-small`).

## Organisation du code

```
cmd/ajean/           point d'entrée
internal/
  home/              emplacements sur le disque, écriture atomique
  config/            réglages
  llm/               client de l'API (discussion en flux, outils, embeddings)
  embedder/          vecteurs de la recherche par le sens : API ou moteur local (installation, lancement)
  tool/              la forme d'un outil
  memory/            profil, leçons, règles, fiches, journal, recherche, rappel, copies du sommeil
  workspace/         le dossier de travail verrouillé
  scripts/           les outils fabriqués par l'assistant (interpréteur intégré)
  tasks/             tâches planifiées : calendrier, exécution, comptes rendus
  apps/              applications créées par l'assistant : versions, document enfermé, pont
  browser/           pilotage du navigateur
  netguard/          refus des adresses locales
  agent/             conversations, boucle modèle ↔ outils, révision, sommeil
  server/            API HTTP et interface web (ui/)
```

```bash
go test ./...
```

## Licence

MIT.
