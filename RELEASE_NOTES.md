Grosse refonte : AJEAN ne gère plus de moteur. Chaque modèle devient un service système qu'AJEAN démarre, arrête et surveille. Les presets existants sont convertis automatiquement. Cette version apporte aussi la dictée vocale, un benchmark réglable et des améliorations du mode Jean.

## Les modèles deviennent des services

- **Un modèle = une commande.** Il suffit d'une ligne de lancement, comme dans un terminal (llama.cpp, ExLlama/TabbyAPI, vLLM ou tout serveur compatible OpenAI), ou d'une API distante. AJEAN déduit l'adresse et la clé de la commande (`--port`, `--api-key`). La commande complète reste visible dans la fenêtre du modèle, avec la clé masquée.
- **Le système fait tourner les modèles** : unité systemd sous Linux, service Windows, agent launchd sous macOS. Le modèle tourne sans session ouverte, et redémarrer l'interface ne le recharge plus.
- **Garde-fou** : avant de démarrer un modèle, AJEAN vérifie la RAM, la VRAM et le port libre. Pendant le chargement, puis en continu, il surveille la RAM et arrête le modèle avant que la machine ne sature.
- **AJEAN n'installe plus rien** : il ne compile plus llama.cpp, ne fournit plus de binaires précompilés, n'installe plus ExLlama ni le moteur MoE intégré, n'utilise plus les GPU cloud et ne télécharge plus de modèles. Le guide `docs/moteurs.md` explique comment brancher chaque moteur.
- Nouvelles commandes `ajean service …` (list, add, start, stop, logs, use, autostart, convert). Les anciennes commandes `switch`, `serve`, `edit`, `gpu`, `llamacpp` et `exl3` sont retirées.

## Passage depuis la 0.18

La migration se fait au premier démarrage, sans action dans la plupart des cas :

- Chaque preset llama.cpp, ExLlama ou API externe devient un modèle, avec la même commande. Le chat reste branché sur le même modèle. Les fichiers d'origine ne sont pas modifiés.
- L'ancien service `ajean-engine` est arrêté et désactivé. Il libère son port et la VRAM.
- Si le modèle démarrait avec la machine, c'est conservé.
- Les conversations, la mémoire, les projets et les fichiers ne sont pas touchés.
- Les presets du moteur MoE intégré et des GPU cloud ne peuvent pas être convertis : un message l'indique, et ils sont à recréer à la main.

**Linux, installation pour un compte utilisateur** : une seule commande reste nécessaire après la mise à jour, car la 0.18 n'avait pas les droits pour installer l'assistant des services. L'interface l'affiche avec le nom exact :

```
sudo ajean install-helper <utilisateur>
```

Le modèle redémarre ensuite tout seul. Une installation en root n'a rien à faire.

## Dictée vocale

- **Bouton micro** à droite du sélecteur de mode, et sur la scène de Jean en plein écran. Une onde audio et un compteur (2 minutes au maximum) s'affichent pendant l'enregistrement ; un carré permet de l'arrêter.
- La transcription passe par une **API Whisper compatible OpenAI** (Groq par défaut, d'autres fournisseurs proposés), réglable dans les paramètres avec la liste des modèles disponibles. Sans clé, AJEAN utilise la dictée intégrée du navigateur.

## Benchmark

- La taille du texte lu (de 2 000 à 100 000 jetons) et celle de la réponse (de 100 à 3 000 jetons) se choisissent avant le test.
- L'avancement s'affiche pendant le test : pourcentage du prefill, puis jetons générés.

## Mode Jean

- **Mise à jour de la mémoire visible** : une pastille indique quand Jean range ou révise sa mémoire, et les outils de Jean ont des libellés lisibles dans le fil.
- **La durée de travail** de Jean s'affiche dans le bouton Arrêter.
- **Option d'apparence** pour masquer les commandes du terminal en plein écran.
- Les icônes message et fichiers sont alignées en plein écran.
- **Tâches planifiées avec script** : le script est rendu exécutable avant d'être lancé (il échouait faute de droits), et ce qu'il affiche est publié dans la conversation de Jean.

## Apps

- L'IA peut **tester une app comme un utilisateur** : cliquer, saisir du texte, choisir une option, appuyer sur une touche, puis voir le rendu obtenu.
- Elle peut aussi **lire et modifier les données d'une app** directement.

## Mise à jour

```
ajean update
```

Testé par les tests automatiques, sur un serveur Linux, et par deux simulations complètes de migration depuis la 0.18.17 sous Linux (installation pour un compte et installation en root, avec redémarrage de la machine). La migration sous Windows et le fonctionnement sous macOS n'ont pas été testés.
