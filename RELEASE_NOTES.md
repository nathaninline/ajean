## Correction

**Un message d'erreur s'affichait avant chaque commande sur les postes Windows sans droits administrateur.** Il ressemblait à ceci, et revenait même pour un simple `ajean help` :

```
[info] dossier de données pas encore migré vers C:\ProgramData\ajean
       (rename ... : Access is denied.) — on continue sur C:\ProgramData\jean
```

Rien n'était cassé. AJEAN fonctionnait normalement, sur son dossier habituel, sans aucune donnée en jeu. Mais renommer `C:\ProgramData\jean` demande le droit d'écrire dans `C:\ProgramData`, qu'un compte standard n'a pas : le renommage échouait à chaque lancement, et le message revenait indéfiniment. C'était un détail interne affiché comme une erreur.

Il ne s'affiche plus. La raison est conservée et ressortie là où elle sert à quelque chose — `ajean where`, qui montre justement les emplacements — avec ce qu'il faut faire pour aligner les noms si vous le souhaitez :

```
ajean where
```

`ajean install` lancé en administrateur termine désormais la migration : c'est le seul moment où les droits nécessaires sont réunis.

Rien de tout cela n'est obligatoire. Un AJEAN qui continue d'utiliser `C:\ProgramData\jean` fonctionne exactement comme les autres, et vos données restent où elles sont.

## Mise à jour

```
ajean update
```

Les binaires restent publiés sous leurs deux noms, `ajean-*` et `jean-*`, le temps que le parc bascule.
