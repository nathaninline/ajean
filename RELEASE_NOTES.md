La 0.21 repart de la 0.18.17 et n'en retire rien : compilation de llama.cpp, binaires précompilés, forks, ExLlama, AJEAN MoE, GPU Cloud, voix et scène de Jean, interrupteur du mode agent et les quatre modes sont tous là. Elle y ajoute ce que les 0.19 et 0.20 avaient apporté de bien, et plusieurs idées de la 1.0.

## Repris de la 0.20

- **Dictée vocale.** Un bouton micro à droite du sélecteur de mode enregistre la voix (2 minutes au maximum, avec une onde et un compteur) et insère le texte dans le champ. La transcription passe par une API Whisper compatible OpenAI (Groq par défaut), réglable dans Paramètres > Dictée vocale ; sans clé, la dictée intégrée au navigateur est utilisée. Elle s'ajoute à la conversation à voix haute du mode Jean, qui ne change pas.
- **Bench réglable.** Le bouton bench ouvre une fenêtre où l'on choisit le nombre de tokens lus (2k, 10k, 25k, 50k, 100k) et générés (100, 300, 1k, 3k). L'avancement s'affiche pendant la mesure et le dernier choix est retenu.
- **Recherche Hugging Face.** Le champ « Télécharger un modèle » cherche les dépôts GGUF : on choisit un dépôt, puis une quantification avec sa taille. Un lien direct fonctionne toujours.
- **Presets API : la liste des modèles du fournisseur et le réglage Réflexion.** Le champ Modèle propose les modèles que l'API annonce. La réflexion se règle (défaut du fournisseur, désactivée, faible, moyenne, élevée) et AJEAN la traduit vers le paramètre du fournisseur reconnu à son adresse. Si le fournisseur la refuse, la requête repart sans elle et c'est indiqué sous la réponse.
- **Apps : l'IA les gère mieux.** Elle peut tester une app comme un utilisateur (cliquer, saisir, choisir une option, appuyer sur une touche, puis voir le rendu) et lire ou modifier directement ses données.
- **Windows.** Relancer AJEAN ramène la fenêtre existante au lieu d'en ouvrir une seconde. Les questions d'installation et de mise à jour passent par la boîte de dialogue actuelle de Windows, nette à toutes les échelles. L'icône est lissée, avec les tailles de la barre des tâches à 125, 150 et 250 %.
- **Moteur AJEAN.** Une version précise de llama.cpp (**b12257**), testée avec cette version d'AJEAN, téléchargée avec vérification de son empreinte, avec ses bibliothèques CUDA. C'est un moteur de plus, à côté du précompilé, du compilé et des forks. Un preset sans `BIN` l'utilise ; un preset qui a déjà son moteur le garde. Il n'est pas téléchargé sur une machine qui a déjà un moteur en service.
- **Presets Service.** Une commande de votre choix (vLLM, un autre llama-server, TabbyAPI, un script) qui expose une API OpenAI sur la machine : AJEAN la lance dans son service moteur et utilise son API. L'adresse est déduite du `--port` de la commande et la clé de son `--api-key`.

## Repris de la 0.19

- **Mode Jean.** Une pastille indique quand Jean range ou révise sa mémoire, les outils de mémoire ont des libellés lisibles dans le fil, et la durée de travail s'affiche dans le bouton Arrêter. Une option d'apparence masque les commandes du terminal en plein écran.
- **Tâches planifiées avec script.** Le script est rendu exécutable avant d'être lancé, et ce qu'il affiche est publié dans la conversation.

## Idées reprises de la 1.0

- **Mot de passe de l'interface.** `ajean password` (ou la section Sécurité) pose un mot de passe : on se connecte une fois par appareil, la session dure 30 jours d'inactivité. Les essais sont freinés (5 libres, puis une attente qui double). La clé de pilotage continue de fonctionner comme avant. `ajean ui listen` et `AJEAN_HOST` choisissent l'adresse d'écoute ; par défaut elle ne change pas.
- **Coffre chiffré des clés d'API.** `ajean secrets on` remplace, dans les presets et les serveurs MCP, chaque clé par une référence vers un coffre chiffré (AES-256). `ajean secrets off` remet les fichiers à l'identique. Éteint par défaut.
- **Validation des tâches.** `ajean review on` : une tâche que l'IA crée, modifie ou supprime attend votre accord, donné une fois en fin de réponse, script compris. Éteint par défaut. C'est un garde-fou contre les surprises, pas une barrière : une IA qui dispose d'un shell peut la lever.
- **Journal des actions.** Chaque outil qui agit est noté avant et après, secrets reconnus masqués (`ajean actions`). Après un tour coupé net, le tour suivant sait ce qui avait été fait. Tenu par défaut ; `ajean actions off` le coupe.
- **Sauvegarde locale chiffrée.** `ajean backup <dossier>` écrit une archive chiffrée par une phrase (base, mémoire, presets, scripts, apps, voix, journal) ; `ajean restore <fichier> <dossier>` la relit dans un dossier vide. `AJEAN_BACKUP_DIR` en fait une par jour.
- **Une adresse par écran.** Chaque écran a son adresse (`#/models`, `#/settings`, `#/apps/<id>`…) : les boutons Précédent et Suivant fonctionnent, un rechargement rouvre le même écran.
- **Une réponse finale.** Quand le modèle se tait après ses outils, une dernière demande part sans outils pour dire ce qui a été fait et ce qui bloque.

## Corrections

- **Le preset actif peut être supprimé.** AJEAN arrête le moteur, supprime le preset et garde les réglages de l'appareil.
- **Téléchargement d'un modèle portant le même nom qu'un autre.** AJEAN compare les tailles et range l'autre modèle sous un nom qui le distingue au lieu de le refuser.
- **Windows, puce graphique intégrée : la jauge de mémoire restait presque vide** pendant qu'un modèle tournait dessus. Elle compte maintenant la mémoire partagée.
- **Texte qui passait soudain en titre ou en gras.** Une balise HTML écrite par le modèle hors bloc de code est affichée telle quelle.
- **Vitesse affichée avec une API distante.** Le débit est calculé sur la durée totale de la requête.
- **Threads.** Ils ne sont plus imposés quand le preset n'en fixe pas : llama.cpp choisit lui-même. Flash attention propose les trois états du moteur (auto, on, off).
- **Un preset n'est marqué actif que si son moteur tourne.**
- **Mises à jour.** Le bandeau « nouvelle version disponible » est vérifié toutes les trois heures, et l'interface se recharge seule après une mise à jour.

## Après la mise à jour

- **Depuis la 0.18.17 : rien à faire.** Presets, moteurs, clés et appareils appairés sont repris tels quels.
- **Depuis la 0.20 :** les presets Local utilisent le moteur AJEAN déjà installé ; les presets Service restent valides ; les presets ExLlama, MoE et GPU Cloud, grisés en 0.20, redémarrent. Une ligne `BIN` restée dans un preset et qui ne pointe pas sur le moteur AJEAN est mise en commentaire. Le mode agent et l'accès internet sont activés s'ils n'étaient pas réglés.
- **Depuis la 0.19 :** les services créés en 0.19 deviennent des presets (Service pour une commande, API pour une adresse). Sous Linux, lancez une fois `sudo ajean install` pour retirer les unités de la 0.19.
- **Retour à une version antérieure :** si le coffre des clés est activé, lancez `ajean secrets off` avant.
- **Derrière un proxy inverse :** posez le mot de passe par `ajean password`, pas depuis l'interface.
- Pas de passage depuis la 1.0 : son format de données est différent.

## Limites connues de cette version

- **Le moteur AJEAN et les presets Service ne se règlent pas encore depuis l'interface.** `ajean engine` installe et répare le moteur ; un preset Service s'écrit dans son fichier (`EXTERNAL=1`, `SERVICE_CMD=…`).
- La sauvegarde locale n'emporte pas encore les images et vidéos des conversations, et fige l'interface pendant l'écriture d'une grosse base.

## Commandes

```
ajean engine [install|repair --variant=cuda|rocm|vulkan|cpu|auto]
ajean password [--remove|status]
ajean ui listen [adresse|all]
ajean secrets [status|on|off]
ajean review [status|on|off|accept|reject]
ajean actions [N|on|off|status]
ajean backup <dossier>
ajean restore <fichier> <dossier>
```

## Mise à jour

```
ajean update
```

Testé par les tests automatiques sous Windows, et à l'écran pour la dictée, le bench, la recherche Hugging Face, les adresses par écran, le mot de passe et les boîtes de dialogue Windows, contre de faux moteurs et de faux fournisseurs. N'ont pas été rejoués sur de vraies machines : le téléchargement du moteur AJEAN et ses variantes (CUDA, ROCm, Vulkan, Metal), un vrai preset Service, la reprise d'une vraie installation 0.19 ou 0.20, Linux et macOS.
