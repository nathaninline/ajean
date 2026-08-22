Version de correction : la mise à jour depuis l'interface web redémarre enfin le service toute seule sous Linux, l'éditeur de preset gagne un sélecteur de modèle de draft pour le décodage spéculatif, et le flash de console au changement de preset sous Windows est corrigé.

## Mise à jour (Linux)

* Le bouton « Mettre à jour » de l'interface web disait « installé ✓ » mais le service ne redémarrait pas : on restait sur l'ancienne version. Le redémarrage automatique utilisait un drapeau (`--no-block`) que la règle sudoers n'autorisait pas, l'ordre échouait donc en silence. C'est corrigé, le service se relance bien après la mise à jour.

> Note : cette correction ne prend effet qu'à partir de CETTE version. En passant d'une version antérieure à la 0.11.2, il faut encore relancer le service une dernière fois à la main (`sudo systemctl restart ajean-ui`) si l'interface reste sur l'ancienne version. Les mises à jour suivantes seront automatiques.

## Éditeur de preset

* Nouveau sélecteur **« Modèle de draft »** pour le décodage spéculatif : quand tu choisis un type à brouillon externe (EAGLE-3, dFlash, dSpark, modèle séparé, et optionnellement MTP), tu peux désigner le petit `.gguf` qui anticipe les jetons. Il est rangé dans le preset et passé au moteur en `--model-draft`. La ligne se masque pour les n-grammes et « aucun ». La liste ne montre que les modèles de draft.

## Windows

* À chaque démarrage du moteur (donc à chaque changement de preset), une fenêtre de console noire clignotait : une vérification interne du binaire llama.cpp n'était pas masquée. Corrigé.

## Cartes graphiques AMD

* Sur les versions récentes d'amd-smi (ex. RX 7800 XT), la carte « GPU / VRAM » affichait tout à zéro : la sortie enveloppe désormais les données dans un objet `gpu_data` qu'AJEAN ne déballait pas. Corrigé, et la température affichée est celle du point chaud (hotspot) plutôt que du bord (issue #39).

## Accès distant

* Sur `app.ajean.link`, un bandeau prévient quand le serveur AJEAN de la machine n'est pas à jour, avec un bouton pour lancer la mise à jour.

## Mettre à jour

```
ajean update
```
