Lisibilité de la compilation Windows et désinstallation d'un moteur.

## Journal de compilation lisible

Depuis le passage à Ninja, la sortie du compilateur (cl.exe) faisait remonter dans l'interface des milliers d'avertissements et de notes de gabarit, parfois avec des accents mal encodés. Le journal affiché ne conserve désormais que la progression et les vraies erreurs ; la sortie complète reste enregistrée dans le fichier de log pour le diagnostic.

## Retour pendant l'installation des outils

Lorsqu'un outil manquant (par exemple Ninja) est installé automatiquement, l'interface l'indique maintenant explicitement au lieu de sembler figée sur « vérification des outils ». L'étape en cours est affichée en clair.

## Indicateur d'activité

L'émoji sablier utilisé pendant une compilation est remplacé par un indicateur circulaire discret, cohérent avec le reste de l'interface.

## Désinstaller un moteur

Une commande de terminal permet de supprimer un moteur installé :

```
ajean llamacpp uninstall compiled
ajean llamacpp uninstall prebuilt
ajean llamacpp uninstall custom <nom>
```

La suppression du moteur actif est refusée par défaut ; l'option `--force` arrête le service et libère la sélection.

## Mise à jour

```
ajean update
```
