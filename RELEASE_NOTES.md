Le poste distant est maintenant intégré à AJEAN : plus de binaire séparé, plus de téléchargement à part.

## Une seule application

Jusqu'ici, piloter un autre PC demandait d'installer un second binaire, « ajean-remote ». C'était une application de plus à télécharger, à suivre et à mettre à jour. On l'a supprimée : tout vit désormais dans **AJEAN**.

Sur le PC à piloter, tu installes simplement AJEAN, puis tu lances :

```
ajean remote install https://ajean.link --machine <id> --key <clé> --code <code> --allow shell,read,write,list
```

La commande s'installe en service — invisible, au démarrage, reconnexion automatique — exactement comme avant. Rien ne change au chiffrement : l'accès reste **chiffré de bout en bout**, le relais ajean.link ne voit toujours rien.

## Rien à changer dans ton usage

Dans **Réglages → Postes distants**, générer un code affiche la commande prête à copier — à distance (via ajean.link) ou en réseau local. Seul le nom de la commande change : `ajean remote install …` au lieu de `ajean-remote install …`.

Les postes déjà appairés continuent de fonctionner sans réappairage.
