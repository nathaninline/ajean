Compilation de llama.cpp sur Windows nettement plus fiable.

## Compilation Windows via Ninja

Sur Windows, la compilation de llama.cpp passait par le générateur Visual Studio de CMake. Ce choix rendait le build dépendant de deux éléments fragiles : la présence d'une édition de Visual Studio dont le nom correspond exactement (« Visual Studio 17 2022 »), et, pour CUDA, l'intégration MSBuild « CUDA x.y.props » installée au bon endroit et à la bonne version. Selon la machine, l'une ou l'autre manquait et la configuration échouait sur un message opaque (« could not find any instance of Visual Studio », « No CUDA toolset found », suivi de « exit status 1 »).

La compilation utilise désormais le générateur Ninja, qui invoque le compilateur et nvcc directement, sans passer par MSBuild. L'environnement du compilateur est mis en place automatiquement, et Ninja est installé au besoin. Cela supprime cette catégorie d'erreurs de configuration.

## Détection de Visual Studio et de CUDA

La détection de l'édition de Visual Studio ne se limite plus à une table figée : l'année de la gamme de produit est lue directement, ce qui prend en charge les versions récentes (dont Visual Studio 2026) sans mise à jour.

Le choix de la version du toolkit CUDA lorsque plusieurs sont installées se fait maintenant par comparaison numérique des versions, et non plus alphabétique : la version réellement la plus récente est retenue.

## Parallélisme adapté à la mémoire

Le parallélisme des compilations CUDA est plafonné en fonction de la mémoire disponible. Sur une machine à nombreux cœurs mais mémoire limitée, la compilation ne sature plus la RAM au risque d'un échec en cours de route.

## Mise à jour

```
ajean update
```
