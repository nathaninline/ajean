Version corrective, dans la foulée de la 0.7.9. Elle répare la mise à jour elle même sous Windows, et un affichage parasite dans l'éditeur de modèle.

## La mise à jour sous Windows ne se plaint plus des droits à tort

Le bouton de mise à jour de l'interface pouvait échouer avec « droits insuffisants pour remplacer C:\ProgramData\ajean\bin\ajean.exe, relance AJEAN en administrateur puis réessaie ». Le conseil était faux : aucun privilège, administrateur compris, ne permet de remplacer l'image d'un exécutable en cours d'exécution. Relancer en administrateur ne changeait donc rien.

La vraie cause : pour remplacer son binaire, AJEAN écarte l'ancien sous le nom `ajean.exe.old`. Si un processus tournait encore depuis ce fichier, reste d'une mise à jour précédente, il ne pouvait être ni supprimé ni écrasé. Windows renvoie alors le code « accès refusé », que Go rapporte comme un défaut de droits, d'où le message trompeur.

L'ancien binaire est désormais écarté sous un nom unique et horodaté : un reste verrouillé ne bloque plus rien. Les trois endroits qui effectuaient cette manœuvre (mise à jour, installation, premier lancement) en profitent, et le ménage au démarrage ramasse aussi bien l'ancien nom fixe que les nouveaux. Quand un remplacement échoue malgré tout, le message nomme les deux causes possibles au lieu d'en affirmer une seule, puisque Windows utilise le même code d'erreur pour « droits insuffisants » et « fichier utilisé ».

À noter : cette mise à jour ci doit encore être installée par la version précédente. Elle passera sans problème si aucun ancien processus AJEAN ne traîne. Si le message d'erreur revient, fermez l'application et arrêtez le service, puis réessayez.

## La section « Cartes graphiques » ne s'affiche plus à tort

Introduite en 0.7.9, elle n'a de sens qu'à partir de deux cartes. Elle restait pourtant visible sur une machine mono GPU : le masquage passait par l'attribut `hidden`, sans effet ici car la mise en page du groupe l'emportait sur la règle par défaut du navigateur.

## Mise à jour

```
ajean update
```

Le comportement du remplacement de binaire a été reproduit puis vérifié corrigé sur Windows, avec deux anciens processus maintenus en vie. La correction d'affichage a été vérifiée sur le rendu réel, pas seulement sur l'état interne.
