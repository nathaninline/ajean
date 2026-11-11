La 0.20 repart de la 0.18.17 et change une seule chose, en profondeur : le moteur. AJEAN fonctionne désormais comme Ollama, avec une version précise de llama.cpp livrée et testée pour chaque version d'AJEAN.

## Moteur

- **Un seul moteur, toujours le même pour tout le monde.** AJEAN 0.20.0 livre llama.cpp **b12257**. Il n'y a plus de moteur à choisir, à compiler ou à mettre à jour : le moteur change uniquement avec les mises à jour d'AJEAN, après avoir été testé avec.
- **Installation automatique.** Le moteur se télécharge tout seul au premier lancement (et après une mise à jour d'AJEAN qui change de version de llama.cpp). Chaque archive est vérifiée par son empreinte SHA-256 inscrite dans AJEAN.
- **La bonne variante, vérifiée avant usage.** CUDA (NVIDIA), ROCm (AMD sous Linux), Vulkan (AMD, Intel), Metal (Mac) ou CPU. AJEAN essaie la variante la plus rapide dans un processus à part ; si elle ne démarre pas ou ne voit aucune carte, il passe à la suivante. Les bibliothèques CUDA sont livrées avec le moteur : seul le pilote de la carte est nécessaire, plus de CUDA Toolkit à installer.
- **Section Moteur simplifiée.** Elle montre la version et la variante en place, et propose deux gestes de dépannage : réparer (retélécharger) et imposer une variante.
- **Erreurs lisibles.** Quand le moteur s'arrête, AJEAN affiche la ligne d'erreur de llama.cpp (mémoire insuffisante, modèle illisible, argument inconnu…) au lieu d'un chargement sans fin.

## Presets : Local, API ou Service

- **Trois façons de faire tourner un modèle**, au choix dans l'éditeur de preset : **Local** (le llama.cpp d'AJEAN et un `.gguf`), **API** (un point d'accès compatible OpenAI) ou **Service**.
- **Service** : une commande de votre choix (vLLM, un autre llama-server, TabbyAPI, un script…) qui expose une API OpenAI sur la machine. AJEAN la lance dans son service moteur, à la place de llama.cpp, et utilise son API : démarrage, arrêt, journal et relance en cas d'échec sont ceux du moteur. L'adresse est déduite du `--port` de la commande, le modèle est le premier annoncé par l'API.
- **Presets API : la liste des modèles du fournisseur.** On saisit l'URL, puis la clé, et le champ Modèle propose les modèles que le fournisseur annonce. Si la liste est indisponible, ou via « autre identifiant », la saisie libre reste possible. Vaut aussi pour un Service déjà lancé.
- **Presets API et Service : réglage Réflexion.** Défaut du fournisseur, désactivée, faible, moyenne ou élevée. AJEAN traduit le niveau vers le paramètre du fournisseur reconnu à son adresse (Alibaba/Qwen, OpenRouter, Z.ai, DeepSeek, serveur local ; champ OpenAI `reasoning_effort` pour les autres). Si le fournisseur refuse ce paramètre, la requête repart sans lui, c'est indiqué sous la réponse, et « tester la connexion » le signale. Le raccourci de niveau du champ de saisie fonctionne aussi avec ces presets.
- **Recherche Hugging Face.** Le champ « Télécharger un modèle » cherche les dépôts GGUF sur Hugging Face : on choisit un dépôt, puis une quantification (avec sa taille), et AJEAN la télécharge. Un lien direct fonctionne toujours.

## Repris de la 0.19

- **Dictée vocale.** Un bouton micro à droite du sélecteur de mode enregistre la voix (2 minutes au maximum, avec une onde et un compteur) et insère le texte dans le champ. La transcription passe par une API Whisper compatible OpenAI (Groq par défaut), réglable dans Paramètres > Dictée vocale ; sans clé, la dictée intégrée au navigateur est utilisée.
- **Apps : l'IA les gère mieux.** Elle peut tester une app comme un utilisateur (cliquer, saisir du texte, choisir une option, appuyer sur une touche, puis voir le rendu), et lire ou modifier directement les données d'une app.
- **Deux modes au lieu de quatre** : Projet et Jean. Les modes Rapide et Modèle de base sont retirés ; un appareil qui y était réglé passe en Projet, et leurs anciennes conversations restent dans l'historique.
- **Bench réglable.** Le bouton bench du preset actif ouvre une fenêtre où l'on choisit le nombre de tokens lus (prefill : 2k, 10k, 25k, 50k, 100k) et générés (decode : 100, 300, 1k, 3k). Pendant la mesure, l'avancement s'affiche en pourcentage, phase par phase. Le dernier choix est retenu.

## Corrections et finitions

- **Windows, puce graphique intégrée (Intel, Radeon des Ryzen) : la jauge de mémoire restait presque vide** pendant qu'un modèle tournait dessus. Elle ne comptait que la mémoire « dédiée », qu'une puce intégrée n'a pas ; elle compte maintenant la mémoire partagée.
- **Icône Windows plus nette** dans la barre des tâches : bords lissés, tailles 20, 24 et 40 px ajoutées, et la fenêtre de l'application reçoit des icônes PNG à chaque taille.
- **Le mode agent est actif d'office**, sans fenêtre de confirmation : tous les modes reposent sur ses outils. `ajean agent off` le coupe toujours.
- **L'accès internet de l'IA est toujours actif** : l'interrupteur disparaît des paramètres.
- **Réglages des presets locaux revus pour llama.cpp b12257.** Flash attention propose les trois états du moteur (auto, on, off) au lieu d'un interrupteur dont la position éteinte voulait dire « auto ». Les threads ne sont plus imposés quand le preset n'en fixe pas : llama.cpp choisit lui-même (les cœurs performants sur un processeur hybride) au lieu de prendre tous les cœurs logiques. Choisir un effort de réflexion n'ajoute plus `--jinja`, actif par défaut.
- **Téléchargement d'un modèle portant le même nom qu'un autre.** Deux dépôts publient souvent leurs fichiers sous des noms identiques (un modèle et sa variante MTP, par exemple) : AJEAN refusait le second avec « le modèle existe déjà ». Il compare maintenant les tailles et enregistre l'autre modèle sous un nom qui le distingue (`…-MTP.gguf`).
- **Le preset actif peut être supprimé.** AJEAN refusait (« preset actif, switche d'abord »), ce qui obligeait à en créer un second pour supprimer le premier. Il arrête maintenant le moteur, supprime le preset et, si demandé, le fichier du modèle.
- **Menu allégé.** Les sections Configuration et Moteur disparaissent : tout se lit sur le preset. Arrêter, redémarrer ou démarrer le moteur se fait depuis le bouton « moteur » dans les réglages du preset actif (à côté du bench), qui permet aussi de copier la commande de lancement. L'installation du moteur se suit par une pastille, qui ouvre son journal.
- **Une seule fenêtre AJEAN.** Relancer AJEAN alors qu'il tourne déjà (raccourci, fichier téléchargé, « Ouvrir AJEAN » de la zone de notification) ramène la fenêtre existante au premier plan au lieu d'en ouvrir une seconde.
- **Un preset n'est marqué actif que si son moteur tourne.** Moteur arrêté, le preset choisi reste repéré par un trait discret.
- **Vitesse affichée avec une API distante.** Le débit sous la réponse était surestimé : tous les tokens comptés par le fournisseur (réflexion non diffusée comprise) étaient rapportés à la seule durée du texte visible. Il est maintenant calculé sur la durée totale de la requête. Rien ne change pour un modèle local ou un service sur la machine.
- **Texte qui passait soudain en titre ou en gras.** Une balise HTML écrite par le modèle hors bloc de code (« un <h3> par groupe ») était interprétée et avalait la suite du message. Le HTML brut est maintenant affiché tel quel.
- **Presets API et Service : plus de section Échantillonnage** ni de configuration brute. Ces réglages ne concernent que le llama.cpp d'AJEAN ; un serveur distant ou une commande Service a les siens.
- **Windows : mise à jour en lançant le fichier téléchargé.** La question « Mettre à jour AJEAN ? » passe par la boîte de dialogue actuelle de Windows, nette à toutes les échelles d'écran, au lieu de l'ancienne boîte floue. Après validation, la fenêtre d'AJEAN revient au premier plan (elle restait derrière, ce qui donnait l'impression qu'AJEAN ne se relançait pas) et se recharge sur la nouvelle interface.
- **Mises à jour.** Le bandeau « nouvelle version disponible » n'était vérifié qu'à l'ouverture de la fenêtre : il l'est maintenant toutes les trois heures. Après une mise à jour, l'interface se recharge toute seule dès que la nouvelle version répond (elle restait sur l'ancienne interface jusqu'à un rechargement manuel), et le modèle en cours est relancé sur le nouveau moteur.

## Retiré

- **GPU Cloud** (Modal).
- **Le prompt système des presets.** Un projet a sa description, et Jean apprend seul. Un prompt enregistré sur un preset avec une version précédente n'est plus appliqué.
- **Le plein écran du mode Jean et son personnage animé** : le mode Jean affiche le fil de discussion habituel.
- **La conversation vocale du mode Jean** (micro, voix de Jean, réglages « Voix de Jean »).
- **ExLlamaV3** (modèles EXL3) et **AJEAN MoE**.
- **La compilation locale de llama.cpp**, les **moteurs personnalisés** (forks) et le choix du moteur par preset.
- Les commandes `ajean llamacpp …` et `ajean exl3 …`, remplacées par `ajean engine`.

## Après la mise à jour

- Les presets GGUF continuent de fonctionner sans rien changer : l'ancienne ligne `BIN=` est ignorée.
- Les presets ExLlama, MoE et GPU Cloud restent dans la liste, grisés, et ne démarrent plus.
- Les dossiers des anciens moteurs (llama.cpp compilé, forks, ExLlama) ne sont pas supprimés automatiquement : `ajean engine` et la section Moteur les listent, vous pouvez les effacer pour récupérer de la place.
- Un modèle qui exigeait un fork de llama.cpp (quantification spéciale) n'est plus pris en charge en Local ; il peut tourner en Service avec son propre serveur.
- **Depuis la 0.19 : vos modèles sont repris automatiquement**, au premier démarrage. Les presets de la 0.18 n'avaient pas été supprimés par la 0.19 : ils reviennent tels quels. Les services créés en 0.19 deviennent des presets (Service pour une commande, API pour une adresse), avec leur clé, leur contexte et leurs réglages de génération. Le modèle que le chat utilisait redevient le preset actif. `services.json` est conservé.
- **Depuis la 0.19, sous Linux : lancez une fois `sudo ajean install`.** La 0.19 avait désactivé le service du moteur (`ajean-engine`) et créé une unité système par modèle (`ajean-svc-…`) ; cette commande remet le premier et retire les secondes. Sous Windows, si un modèle de la 0.19 tourne encore comme service Windows, `ajean install` dans un terminal administrateur l'arrête.
- **Le prompt système par preset n'existe plus** (voir Retiré) ; le prompt enregistré reste dans la base mais n'est plus envoyé.
- **Depuis la 0.18 ou la 0.19, rechargez la fenêtre d'AJEAN après la mise à jour** (ou fermez-la et rouvrez-la) : l'ancienne interface ne sait pas encore se recharger seule.

## Commandes

```
ajean engine                      # version livrée, variante installée
ajean engine repair               # retélécharger le moteur
ajean engine repair --variant=vulkan   # imposer une variante (auto pour revenir)
```

## Mise à jour

```
ajean update
```

Testé par les tests automatiques, et sur Windows 11 avec une carte Intel (variante Vulkan : installation automatique, réparation, changement de variante). Un modèle GGUF a été chargé et interrogé avec llama.cpp b12257 sur cette même machine. Les variantes CUDA, ROCm et Metal, et le lancement d'un vrai Service (vLLM…), n'ont pas encore été rejoués sur de vraies machines.
