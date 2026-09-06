Interface multilingue (anglais par défaut, français disponible), possibilité de consulter d'autres conversations et projets pendant qu'une génération est en cours sans l'interrompre, et correction d'une perte silencieuse des images consultées lors d'un compactage.

## Nouveautés

* **Interface multilingue.** L'interface est désormais traduite intégralement en anglais (langue par défaut) et en français. La langue se change en direct, sans rechargement, via un sélecteur dans la section Apparence. Le choix est conservé et synchronisé entre les appareils.
* **Consulter d'autres conversations pendant une génération.** Il est maintenant possible d'ouvrir une autre conversation ou un autre projet en lecture seule alors qu'une réponse est en cours de génération, sans la couper. La génération se poursuit côté serveur, de façon autonome, et redevient visible en direct au retour. Une pastille de notification signale, sur l'icône du projet et sur la conversation concernée, qu'une réponse a été générée et pas encore consultée. Le retour à la conversation en cours se fait naturellement en cliquant dessus ou dans la zone de saisie.
* **Section « Actions ».** L'ancienne section « Réglages » de la barre latérale est renommée « Actions » et accueille un nouveau bouton « Nouvelle conversation », qui démarre une session vierge dans le projet actif.
* **Hub des projets plus fluide.** Le changement de projet est désormais instantané, et le chargement de la liste des conversations est signalé par un indicateur visuel, sans à-coups.

## Corrections

* Les images consultées par l'outil de vision pouvaient disparaître silencieusement du contexte lors d'un compactage, laissant le modèle poursuivre son raisonnement sans elles et sans en avoir conscience. Leur présence est désormais préservée dans le résumé, et une éventuelle perte est rendue visible au lieu de passer inaperçue, afin que le modèle sache qu'il peut de nouveau consulter l'image plutôt que de continuer à raisonner à l'aveugle.

## Mise à jour

```
ajean update
```
