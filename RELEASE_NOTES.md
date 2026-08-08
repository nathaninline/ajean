Deux bugs signalés par l'usage, tous les deux confirmés, tous les deux réparés. Le premier pouvait bloquer le chat jusqu'au redémarrage du service. Le second faisait mentir un interrupteur.

## Le bouton stop arrête vraiment, et le chat ne se bloque plus

Le scénario, tel qu'il était vécu : le modèle lance une commande, elle dure, on clique sur stop, rien ne se passe. On redémarre alors le moteur, et là le chat reste figé avec son bouton stop, sans plus rien accepter. Vider la conversation ne suffit pas, rafraîchir la page non plus ; il faut redémarrer le service d'interface.

Trois défauts se cumulaient, et ils sont corrigés ensemble.

**La commande ignorait l'arrêt.** Elle démarrait avec un contexte à elle, indépendant du tour. Annuler la génération n'annulait donc rien du tout : le tour restait suspendu jusqu'au bout du délai, cinq minutes au maximum. La commande hérite désormais du contexte du tour, et stop la tue pour de bon. Dans la foulée, un arrêt demandé au milieu d'une série d'appels d'outils interrompt la série au lieu de la dérouler jusqu'au bout.

**Une commande qui laisse un processus en arrière-plan bloquait le tour pour toujours.** Quelque chose comme `./serveur &` rend la main tout de suite, mais le processus détaché garde les tubes de sortie ouverts, et l'attente de fin de commande attendait leur fermeture, c'est à dire jamais. Ni le délai ni le stop n'en venaient à bout. L'attente est maintenant bornée après la fin ou la mise à mort du processus.

**Vider la conversation ne débloquait pas.** C'est pourtant le geste qu'on tente quand le chat est coincé. « Nouvelle conversation » libère désormais toujours l'état de génération, quel que soit le sort du tour abandonné. Plus besoin de redémarrer quoi que ce soit.

Au passage, un tour abandonné qui se termine après le démarrage du suivant ne vient plus déclarer ce dernier terminé.

## Un troisième défaut, trouvé en écrivant les tests

Le dossier de travail de l'agent est résolu une fois pour toutes au démarrage. S'il disparaissait ensuite, parce que vous avez fait le ménage ou parce que le modèle l'a supprimé lui-même, **toutes** les commandes suivantes échouaient sur un « chdir : no such file or directory » incompréhensible, et ce jusqu'au redémarrage. Il est maintenant recréé au besoin.

## Raisonnement désactivé veut enfin dire désactivé

Couper le raisonnement dans l'éditeur de preset **effaçait** la ligne `REASONING` au lieu d'écrire `off`. Ce n'est pas la même chose : sans consigne, le moteur suit le gabarit du modèle, et un modèle à raisonnement raisonne. L'interrupteur affichait donc « désactivé » pendant que le modèle réfléchissait tranquillement.

L'interface écrit maintenant `on` ou `off`, explicitement, et `off` est transmis au moteur comme une interdiction.

Avec une précaution : le drapeau qui désactive le raisonnement est récent, et certains moteurs, notamment le fork ik_llama.cpp, ne le connaissent pas. Le leur passer les ferait refuser de démarrer, donc boucler. AJEAN demande au binaire ce qu'il sait faire avant de le lui passer, et le dit dans le journal quand le moteur choisi ne permet pas de couper la réflexion.

Vos presets existants ne sont pas modifiés. Ceux dont la ligne est absente gardent le comportement d'avant, et l'éditeur ne prétend plus que le raisonnement y est coupé : il indique que rien n'est précisé et que le modèle décide. Basculez l'interrupteur une fois pour trancher.

## Mise à jour

```bash
ajean update
```

Puis, si vous utilisez l'accès distant, rechargez la page du portail pour prendre la nouvelle interface.
