Quelques réglages d'interface pour une lecture plus claire, et un statut du moteur enfin honnête.

## Un statut du moteur plus juste

La pastille d'état affichait « modèle incompatible » dès qu'un chargement échouait — même quand la vraie cause était ailleurs. Désormais elle reste courte et neutre (**prêt**, **chargement…**, **erreur**, **arrêté**), et le message détaillé en dessous n'accuse le moteur d'incompatibilité **que lorsque le journal le montre vraiment** (quantification ou architecture non reconnue). Pour tout autre échec, il renvoie simplement au journal du moteur au lieu de donner une fausse piste.

## Panneau « Actions » réorganisé

Le bouton « refresh », qui ne servait à rien, est retiré. Les actions sont regroupées deux par deux : **mise à jour** et **exporter** sur une ligne, **bench** et **effacer la conversation** sur l'autre.

## Petits nettoyages

Le titre de l'éditeur de preset n'affiche plus le mot « Preset » redondant (juste le nom), et le mode mémoire automatique s'appelle simplement « auto ».
