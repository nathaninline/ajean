Le mode Jean sort de la bêta.

## Jean, assistant personnel

Après plusieurs semaines d'utilisation quotidienne, le mode Jean n'est plus marqué « Beta ». Pour rappel, Jean :

- garde **sa propre mémoire** : un profil court, des fiches (procédures, guides) et un journal de chaque échange, consultables et modifiables depuis la fenêtre *Mémoire de Jean* ;
- **apprend de ses erreurs** : une correction devient une leçon qui ne se répète plus, et une tâche laborieuse est rangée en fiche et en script pour aller plus vite la fois suivante ;
- **range sa mémoire la nuit** (consolidation), avec un garde-fou qui restaure tout ce qui aurait été perdu ;
- **repart d'un contexte vide** après 3 heures sans message (clé `JEAN_IDLE_HOURS`, `0` = jamais), après avoir relu l'ancien fil pour ne rien oublier. Le fil affiché et le journal ne bougent pas ;
- travaille dans **son propre espace**, sans pouvoir modifier les scripts, fichiers ni la mémoire des projets.

## Corrections

- **Compactage relancé en boucle.** Au cours d'une très longue tâche, quand le résumé de l'historique échouait, la compaction était retentée à chaque étape, chaque fois avec un résumé complet voué au même échec. Après un échec, elle attend désormais que le contexte ait nettement grandi avant de réessayer.
- **Cause des résumés ratés.** Un résumé en erreur ou vide est maintenant inscrit dans le journal du service, avec sa cause, au lieu de passer inaperçu.

## Mise à jour

    ajean update

Vérifié par les tests automatiques et par l'historique d'utilisation du mode Jean sur le serveur de test (révisions, consolidations de nuit, vidage du contexte). Non testé en conditions réelles : la nouvelle temporisation du compactage pendant une longue tâche.
