Corrections importantes (plantage après un compactage, raisonnement sur API externe, mémoire GPU sur portables AMD), conversation à voix haute avec Jean et gestionnaire de fichiers du mode Jean.

## Corrections

- **« Erreur d'envoi » puis redémarrage du service dans une longue conversation.** Quand le contexte était compacté au milieu d'un tour, AJEAN relisait ensuite les messages de ce tour à une position qui n'existait plus, et le service plantait. Les messages du tour sont maintenant mis de côté avant le compactage. Merci pour le rapport détaillé (#181).
- **Preset API externe : l'effort de réflexion était envoyé dans un champ propre à llama.cpp** (`chat_template_kwargs`). Les API qui valident strictement la requête (OpenAI, Mistral, Groq…) pouvaient la refuser, et les autres l'ignoraient. Vers une API externe, AJEAN envoie désormais le champ standard `reasoning_effort` ; rien ne change pour llama.cpp en local. Merci pour l'analyse (#178).
- **Windows, portable avec processeur Ryzen et carte Radeon : mémoire GPU inversée.** La puce graphique intégrée (« AMD Radeon(TM) Graphics ») était prise pour une carte dédiée et recevait la mémoire et l'utilisation de la vraie carte, et inversement. Les puces Radeon intégrées sont maintenant reconnues comme telles. Merci pour les mesures (#182).
- **Mode Jean : le mot d'activité des tours terminés continuait de s'animer** (« Retient ça… ») et cachait la durée, les tokens et la vitesse du tour. Il est vidé à la fin de chaque tour. Correctif repris de la proposition #173, merci.

## Mode Jean

- **Parler à Jean à voix haute.** Un bouton micro sur la scène de Jean ouvre une conversation mains libres : une courte pause suffit pour envoyer, la réponse est lue dès sa première phrase écrite, et Jean annonce aussi ce qu'il fait quand il utilise un outil. Toucher l'écran l'interrompt. Le texte dicté entre dans la conversation habituelle (mémoire, outils, fil). Pour un message dicté, Jean répond en phrases courtes, comme à l'oral.
- **Réglages : Paramètres > Voix de Jean.** Activation, adresse du serveur audio, choix et écoute de la voix, création d'une voix à partir d'une description, sensibilité du micro et durée de la pause avant envoi. Les voix sont enregistrées dans AJEAN (dossier `voices`).
- La voix nécessite un **serveur audio.cpp** (transcription Qwen3-ASR et synthèse VoxCPM2) lancé à part : AJEAN ne l'installe pas encore. Sans lui, le bouton micro n'apparaît pas.
- **Fichiers de Jean.** Une fenêtre (bouton dossier sur la scène) permet de parcourir, envoyer, télécharger et supprimer les fichiers de son dossier de travail, et de lui en confier un. Jean retrouve un fichier par ce qu'il contient (PDF, documents, texte), pas seulement par son nom.

## Mise à jour

```
ajean update
```

Testé par les tests automatiques, et sur un serveur Linux pour la voix et les fichiers. Les corrections #181, #178 et #182 n'ont pas été rejouées sur les machines des rapports (Windows, API externe stricte, portable Ryzen + Radeon).
