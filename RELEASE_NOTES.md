Cette version soigne l'affichage des images dans le chat et ajoute un réglage pour économiser la VRAM quand la vision est active.

## Images dans le chat

* Quand tu joins une image, tu vois maintenant une vignette (un petit aperçu) au lieu du seul nom de fichier, aussi bien dans la zone de saisie que dans la bulle du message une fois envoyé.
* Envoyer une image sans écrire un mot ne laisse plus une bulle vide sous la pièce jointe : seule la vignette reste.

## Éditeur de preset

* Nouveau réglage « Projecteur vision sur le CPU » : il garde le projecteur multimodal (mmproj) hors du GPU et libère la VRAM qu'il occupait, utile quand un gros modèle remplit déjà la carte. Le réglage n'apparaît que si une vision est sélectionnée, pour ne pas encombrer.

## Mise à jour

Depuis un terminal :

```
ajean update
```

Testé sur le serveur Linux (déploiement direct). Le réglage « projecteur sur le CPU » a été vérifié côté génération d'arguments, mais son effet exact sur la VRAM dépend de ta configuration matérielle.
