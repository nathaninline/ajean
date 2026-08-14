La grande nouveauté de cette version : AJEAN sait enfin voir les images.

## La vision, configurable par preset

Jusqu'ici rien dans l'interface ne permettait de donner des yeux à un modèle. Il fallait glisser l'option `--mmproj` à la main dans la configuration brute, et son chemin n'était même pas résolu comme celui du modèle. L'éditeur de preset gagne un champ **Vision** : tu y choisis le projecteur multimodal (fichier `mmproj`) qui accompagne le modèle, et c'est tout. Au démarrage du moteur, AJEAN le charge tout seul.

Le champ ne liste que les projecteurs (les fichiers dont le nom contient `mmproj`), pas les modèles de plusieurs Go, pour que le choix reste lisible. Et s'il te manque un projecteur, le champ « télécharger un modèle » accepte aussi un lien vers un `mmproj` : une fois récupéré, il se place directement dans le champ Vision.

## Les images arrivent vraiment au modèle

Avant, une image collée dans le chat était simplement déposée comme un fichier dans le dossier de travail, à charge pour le modèle de l'ouvrir avec ses outils (ce qui ne donnait qu'un tas d'octets illisibles). Désormais, quand un projecteur est configuré, l'image part au modèle en contenu multimodal : il la voit, et peut en parler.

## Mise à jour

```
ajean update
```

Non vérifié sur cette version : le résultat final dépend d'un modèle vision et de son projecteur compatibles (Qwen2.5-VL, Gemma 3, etc.). À noter, l'image reste dans l'historique de la conversation et repart au moteur à chaque tour tant que la conversation dure.
