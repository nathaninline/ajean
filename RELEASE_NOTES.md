Cette version répare la recherche sur internet. Quand une recherche devenait longue, l'IA tournait en rond : elle rouvrait sans fin la même page, repartait de zéro, ou répondait soudain à une question posée bien plus tôt. Ce n'était pas le modèle, c'était le compactage du contexte qui effaçait exactement ce dont elle avait besoin.

## L'IA ne repart plus de zéro au milieu d'une recherche

Quand la conversation devient trop longue pour la fenêtre de contexte, AJEAN la compacte : les vieux tours sont remplacés par un résumé. Ce résumé était fabriqué à partir d'un historique dont on avait d'abord effacé tous les résultats d'outils. Autrement dit, chaque page web lue avait déjà disparu quand le résumé était écrit. Le résumé ne pouvait donc contenir aucune des informations trouvées, seulement la trace que des recherches avaient eu lieu.

Résultat vu de l'extérieur : l'IA relançait les mêmes recherches en boucle, sans jamais aboutir.

Le résumé est maintenant écrit à partir des vraies pages lues. On lui demande explicitement de conserver les informations récoltées (faits, chiffres, dates, adresses consultées) et de dire où en est le travail : ce qui est répondu, ce qui manque, la prochaine étape.

## La question en cours ne se perd plus

Pendant une recherche, un tour peut enchaîner des dizaines d'appels d'outils sans le moindre message de votre part. Le compactage protégeait les tours récents et le tout premier message de la conversation, mais votre demande en cours, elle, se retrouvait au milieu, et disparaissait dans le résumé.

L'IA ne voyait alors plus qu'une seule question explicite : la première de la conversation. Et elle y répondait, en abandonnant la recherche en cours. Votre demande du moment est désormais toujours conservée telle quelle.

## Une même page n'est plus rouverte en boucle

Un appel d'outil rigoureusement identique n'était déjà pas rejoué, mais l'avertissement était collé à la fin du contenu renvoyé, noyé après plusieurs milliers de caractères. L'IA voyait le contenu, pas l'avertissement, et recommençait.

L'avertissement passe en tête, et à partir de la deuxième redemande le contenu n'est plus renvoyé du tout : redemander la même page ne rapporte plus rien et ne consomme plus de contexte. Le tour n'est jamais interrompu pour autant, une recherche qui enchaîne légitimement beaucoup d'appels reste possible.

De même, le message qui remplace un vieux résultat d'outil disait seulement qu'il avait été effacé, ce qui se lisait comme une invitation à retélécharger la page. Il dit maintenant clairement de ne pas le faire.

## Les pages web ne saturent plus le contexte

La lecture d'une page n'avait aucune limite de taille, là où le terminal et les outils MCP en avaient une. Une seule lecture pouvait injecter 25 000 caractères d'un coup et remplir la fenêtre à elle seule, ce qui déclenchait les compactages en cascade décrits plus haut. La lecture est maintenant bornée, avec une indication de comment lire la suite par tranches.

## Le compteur de tokens des outils dit enfin la vérité

Sous chaque appel d'outil, l'étiquette « ~N tok » affichait le plus souvent la même valeur, autour de 1004, quelle que soit la page. Ce n'était pas la taille de la page mais celle du plafond appliqué à l'affichage. Ce plafond est aligné sur ce que voit réellement le modèle : les valeurs affichées varient donc désormais, et correspondent au coût réel.

## Mise à jour

```
ajean update
```

Ou le bouton de l'interface.

Les binaires restent publiés sous leurs deux noms, `ajean-*` et `jean-*`, le temps que les installations existantes basculent.

L'icône de la barre de menus macOS, introduite en 0.6.10, n'a toujours pas été vérifiée sur une vraie machine.
