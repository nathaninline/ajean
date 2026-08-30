Compactage du contexte repensé : les conversations tiennent beaucoup plus longtemps sans perdre le fil, et l'assistant peut rappeler mot pour mot les échanges plus anciens sortis du contexte.

## Conversations longues

* Nouveau système de mémoire de conversation. Quand le contexte se remplit, les tours anciens sont résumés, mais plus rien n'est perdu : les gros blocs (un long code, une page web lue, un résultat d'outil volumineux) sont désormais archivés mot pour mot et adressés par un identifiant court. Le résumé y fait référence au lieu de recopier le bloc, ce qui fait retomber le contexte beaucoup plus bas.

* L'assistant peut aller rechercher lui-même dans l'historique archivé. Deux nouveaux outils lui permettent de rappeler un bloc exact par son identifiant, ou de le retrouver par mots-clés quand il n'est plus listé dans le résumé courant. Un gros fichier fourni plus tôt dans la conversation revient donc à l'identique quand il en a besoin, sans avoir à le refaire.

* L'assistant est conscient de la compaction : il sait que les premiers tours ont été résumés et que le détail reste récupérable. La surface occupée en contexte reste stable même sur une conversation très longue, ce qui permet des échanges quasi ininterrompus.

* Résumé de meilleure qualité. La consigne de résumé produit désormais un vrai texte structuré (demande en cours, informations déjà trouvées, état d'avancement et étape suivante), et un garde-fou écarte un résumé qui reviendrait vide ou dégénéré.

* Le tout premier message de la conversation n'est plus épinglé de force en tête. Après une compaction, l'assistant ne repart plus par erreur sur une ancienne demande à laquelle il avait déjà répondu : il reprend sur la demande réellement en cours.

## Interface

* Bouton d'ajout de preset redessiné : un petit carré arrondi à la croix nette, mieux aligné dans la barre de titre de la section, avec une teinte d'accent au survol.
