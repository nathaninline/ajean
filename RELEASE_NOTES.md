Correctif de compilation de llama.cpp sous Windows : la mise à jour ou l'installation du moteur échouait à l'édition de liens sur certaines machines.

## Corrections

* **Compilation de llama.cpp sous Windows.** Une modification récente de llama.cpp a activé les en-têtes précompilés sur le serveur, ce qui, sous MSVC, faisait échouer l'édition de liens de `llama-server.exe` (erreur `LNK2001 : symbole externe non résolu __`, puis `LNK1120`). Tous les autres binaires se compilaient normalement, mais le serveur restait absent. Le build lancé par AJEAN désactive désormais les en-têtes précompilés sous Windows : `ajean llamacpp install` et `ajean llamacpp update` recompilent sans erreur, y compris pendant une phase transitoire où la branche amont serait de nouveau affectée. Cette protection ne change ni les binaires produits ni le comportement à l'exécution.

## Mise à jour

`ajean update` récupère la nouvelle version. Après mise à jour, `ajean llamacpp install --force` (ou `ajean llamacpp update`) recompile un moteur llama.cpp fonctionnel.
