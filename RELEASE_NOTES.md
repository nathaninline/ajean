Chargement du menu modernisé, moins de requêtes réseau pour les jauges, et réorganisation des actions.

## Indicateurs matériels regroupés

Les jauges VRAM et RAM sont désormais récupérées en un seul appel réseau au lieu de deux, et le sondage périodique s'interrompt quand l'onglet passe en arrière-plan. Le nombre de requêtes diminue, en particulier à travers l'accès distant. Les serveurs plus anciens restent pris en charge : l'interface revient automatiquement aux anciens points d'accès si le nouveau n'est pas disponible.

## Chargement en skeleton

Pendant le chargement, les jauges (VRAM, RAM), la configuration et l'indicateur d'état affichent un skeleton animé plutôt que trois points. L'apparition des données se fait par un fondu, dans le même esprit que le chargement d'une conversation.

## Menu réorganisé

La section « Actions » a été retirée et son contenu redistribué :

- les mises à jour s'affichent automatiquement dans un bandeau en bas du menu ; une vérification manuelle reste possible en cliquant le numéro de version ;
- l'export d'une conversation se fait depuis le hub Projets ;
- le benchmark a rejoint la fenêtre d'édition du preset actif, sous la forme d'une icône.

## Corrections d'interface

Le libellé « API Externe » est corrigé. La fenêtre de benchmark reprend l'animation d'ouverture et le bouton de fermeture communs aux autres fenêtres.
