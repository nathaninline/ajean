Cette version corrige un blocage complet de Windows sur les machines équipées d'un GPU AMD, et plusieurs problèmes d'installation et de démarrage du moteur.

## Windows + GPU AMD : fin des demandes UAC en rafale

Sur un PC Windows avec un GPU AMD et les pilotes Adrenalin, des demandes d'élévation UAC apparaissaient en boucle quelques secondes après le lancement d'AJEAN, sur le bureau sécurisé : la machine devenait inutilisable jusqu'à un arrêt forcé.

La cause : pour afficher la VRAM, AJEAN interrogeait `amd-smi` toutes les 3 secondes. Sous Windows, l'`amd-smi.exe` livré par AMD lance lui-même `diskpart`, qui exige les droits administrateur, d'où une demande UAC à chaque relevé.

AJEAN n'appelle plus jamais `amd-smi` ni `rocm-smi` sous Windows. La carte AMD reste affichée via Vulkan, comme sur les autres cartes non NVIDIA. Sous Linux, `amd-smi` est toujours utilisé, avec moins d'appels : le nom des cartes n'est lu qu'une fois, et un outil en échec n'est retenté qu'au bout d'une minute.

Nouvelle variable d'environnement : `AJEAN_GPU_TELEMETRY=off` coupe toute la télémétrie GPU (aucun outil externe lancé), en cas de souci avec un pilote.

## Télémétrie GPU plus légère sous Windows (AMD, Intel)

Sans carte NVIDIA, chaque relevé de la jauge VRAM lançait le moteur llama.cpp pour lister les GPU, plus un PowerShell, toutes les 3 secondes et pour chaque appareil connecté. Un relevé lent pouvait aussi s'empiler sur le suivant.

Le relevé est désormais partagé entre tous les appareils ouverts, et la liste des GPU n'est relue qu'une fois par minute. La VRAM utilisée et la charge restent à jour à chaque relevé.

## Installation du llama.cpp précompilé : fin de « aucun binaire précompilé adapté »

llama.cpp publie une nouvelle version environ toutes les 30 minutes, et ses fichiers arrivent un par un pendant plusieurs minutes (Windows en dernier). Une installation lancée pendant cette fenêtre échouait avec « aucun binaire précompilé adapté à cette machine », ou installait la version CPU sur un PC équipé d'une carte NVIDIA.

AJEAN ignore maintenant les fichiers encore en cours d'envoi et les versions publiées depuis moins de 20 minutes.

## Plusieurs moteurs sur le même port

Sous Windows, quand l'arrêt du moteur échouait (moteur lancé en administrateur, arrêt demandé sans élévation), un nouveau llama-server démarrait quand même à côté de l'ancien, sur le même port. Les requêtes étaient alors réparties au hasard entre les deux, avec la VRAM saturée et parfois des réponses du mauvais modèle.

Le moteur refuse désormais de démarrer si son port est déjà occupé, avec un message qui explique quoi faire. Un redémarrage dont l'arrêt a échoué le signale au lieu de continuer.

## Cache KV : combinaisons lentes signalées

Le llama.cpp précompilé officiel (CUDA) n'accélère que les caches KV f16, q8_0 et q4_0. Les options q5_1 et les combinaisons K/V mixtes fonctionnent mais sont reconverties en f16 à chaque étape, ce qui ralentit nettement la génération. Ces options portent maintenant la mention « lent en précompilé », et le journal du moteur affiche un avertissement. Un llama.cpp compilé localement par AJEAN n'est pas concerné.

## Mise à jour

    ajean update

Non testé sur du matériel réel : Windows avec GPU AMD (la correction a été vérifiée avec un faux `amd-smi` qui enregistre ses appels) et la version macOS (compilée par la CI, pas vérifiée sur un Mac). Le reste a été vérifié sous Windows et sur un serveur Linux.
