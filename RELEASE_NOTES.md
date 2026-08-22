Version de correction : plus de fenêtre noire qui clignote sous Windows au changement de preset, et les cartes AMD affichent enfin leurs infos avec les versions récentes d'amd-smi.

## Windows

* À chaque démarrage du moteur (donc à chaque changement de preset), une fenêtre de console noire apparaissait puis se refermait aussitôt. C'était une vérification interne du binaire llama.cpp qui n'était pas masquée. Elle l'est désormais : plus aucun clignotement.

## Cartes graphiques AMD

* Sur les versions récentes d'amd-smi (ex. RX 7800 XT sous Bazzite/Fedora), la carte « GPU / VRAM » affichait tout à zéro : la sortie enveloppe désormais les données dans un objet `gpu_data` qu'AJEAN ne déballait pas. C'est corrigé, nom, VRAM, charge et température s'affichent à nouveau (issue #39).
* La température affichée est maintenant celle du point chaud (hotspot/junction) plutôt que du bord (edge) : c'est elle qui déclenche le throttling et qui reflète vraiment l'échauffement de la carte.

## Accès distant

* Sur `app.ajean.link`, un bandeau prévient désormais quand le serveur AJEAN de la machine n'est pas à jour, avec un bouton pour lancer la mise à jour directement.

## Mettre à jour

```
ajean update
```
