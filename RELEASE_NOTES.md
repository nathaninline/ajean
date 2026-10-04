Correctif de la lecture vidéo arrivée en 0.17.11.

## Vision

- **Vidéo ignorée en silence avec certains moteurs.** Une vidéo jointe était envoyée au modèle quel que soit le moteur actif. Les API externes et les moteurs autres que llama.cpp (Strata par exemple) l'ignoraient sans erreur : le modèle ne recevait que le texte du message et cherchait à voir la vidéo avec see_image. La vidéo n'est désormais envoyée directement que si le moteur actif sait la lire (llama.cpp local récent, avec le projecteur vision). Dans les autres cas, elle est présentée au modèle comme un fichier, avec la marche à suivre : extraire quelques images avec ffmpeg, puis les regarder avec see_image. L'outil see_video n'est proposé qu'aux moteurs qui lisent la vidéo.

## Mise à jour

    ajean update

Vérifié sur le serveur Linux (Strata et llama.cpp) et par les tests automatiques.
