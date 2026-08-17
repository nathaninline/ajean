Cette version règle un plafond de contexte trompeur sur les modèles à décodage spéculatif MTP, et sort de l'ombre les réglages moteur qui en étaient la cause.

## Contexte des modèles MTP

Sur un modèle avec tête MTP (décodage spéculatif), le contexte restait bloqué très bas et finissait en mémoire saturée dès qu'on montait, alors que la même carte tenait bien plus sur un autre modèle de taille identique. La cause n'était pas la mémoire vidéo mais un réglage manquant : sans une seule séquence forcée, llama.cpp réservait le cache de contexte pour plusieurs séquences à la fois, ce qui divisait le contexte utile et provoquait la saturation, aggravée par le second cache qu'ouvre le décodage spéculatif.

* Une seule séquence est désormais le défaut sur tous les presets, existants comme nouveaux, sans rien éditer. Un preset peut toujours remonter cette valeur s'il en a besoin.

## Éditeur de preset

Les réglages qui manquaient pour diagnostiquer ce genre de blocage sont maintenant dans le panneau de réglages du preset, plus besoin de passer par la config brute :

* Séquences parallèles, avec la mention qui explique que monter cette valeur multiplie le cache de contexte.
* Cache KV unifié, regroupé avec les autres interrupteurs en bas de la liste.

## Presets

* Petite bulle d'info du contexte (par exemple 32K, 50K, 128K) à côté de la quantization et du benchmark, pour voir la fenêtre de chaque preset d'un coup d'œil.

## Mise à jour

```
ajean update
```

Note : le défaut d'une seule séquence s'applique au prochain lancement de chaque preset. Un preset qui servait volontairement plusieurs requêtes en parallèle doit remonter le réglage à la main.
