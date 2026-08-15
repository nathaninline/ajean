Cette version rend les réglages d'échantillonnage pilotables par preset, et corrige deux soucis d'affichage du chat sur les très longues réponses.

## Les réglages d'échantillonnage, par modèle

Jusqu'ici AJEAN n'envoyait qu'une seule chose au moteur : la température, figée à 0.7. Tout le reste (top_p, top_k, min_p, pénalité de présence) retombait sur les défauts de llama.cpp, qui ne sont presque jamais ceux que recommande le modèle. Impossible, par exemple, de coller aux réglages conseillés pour Qwen3.8 (température 1.0, top_k 20, min_p 0) sans bricoler.

L'éditeur de preset gagne un bloc **Échantillonnage** : température, top_p, top_k, min_p, pénalité de présence, et un menu **Effort de réflexion** (low, medium, high, xhigh) pour les modèles qui savent doser leur raisonnement. Un champ laissé vide ne change rien : le moteur garde son propre défaut, exactement comme avant. Choisir un effort de réflexion ajoute tout seul l'option nécessaire côté moteur.

## Les longues réponses ne font plus ramer l'app

Sur un très gros raisonnement, l'application se mettait à ralentir de plus en plus : le texte finissait par arriver au compte-gouttes, et il fallait rafraîchir la page pour que tout reparte vite. La cause : à chaque mot reçu, le chat re-analysait la réponse entière, un travail qui grossissait sans fin. Le rendu se cadence désormais selon son coût réel, ce qui garde l'apparition fluide sur une réponse normale et empêche l'app de s'enliser sur une réponse énorme.

## Le bouton stop reste stop

Après un rafraîchissement en pleine génération, le bouton pouvait repasser à « envoyer » alors que le modèle répondait encore. Il se recale maintenant sur l'état réel du serveur, et affiche bien « stop » tant que la réponse n'est pas finie.

## Mise à jour

```
ajean update
```

Non vérifié en conditions réelles : le comportement sur une réponse proche de la limite de contexte (le rendu s'y espace volontairement pour ne pas figer l'interface).
