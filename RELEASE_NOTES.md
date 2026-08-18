Cette version soigne l'affichage du chat : le texte s'écoule de façon fluide pendant la génération, et deux petits défauts de mise en page autour de la zone de saisie sont corrigés.

## Apparition fluide du texte

* Avec le décodage spéculatif (MTP) et surtout la répartition sur plusieurs cartes graphiques, les mots arrivaient par paquets : plusieurs tokens d'un coup, puis une pause, ce qui donnait une lecture saccadée.
* L'affichage est maintenant lissé : les caractères apparaissent à un rythme régulier, quelle que soit la façon dont le moteur les produit. Ça ne bride jamais la vitesse (un modèle rapide reste rapide, un lent reste lent), et le compteur de tokens par seconde reflète toujours la vraie performance.

## Zone de saisie

* Sur une conversation courte, la fin d'une réponse pouvait se glisser sous la zone de saisie sans qu'on puisse la faire remonter. Le fil garde désormais toujours assez d'espace en bas, même quand la saisie s'agrandit sur plusieurs lignes ou qu'un fichier est joint.
* La barre de défilement était masquée par le dégradé du bas et devenait difficile à attraper. Elle reste maintenant saisissable jusqu'en bas.

## Mise à jour

Depuis un terminal :

```
ajean update
```

Changements d'interface uniquement, aucun impact sur le moteur ni sur les modèles installés.
