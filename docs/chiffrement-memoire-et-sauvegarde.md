# Chiffrement de la mémoire + sauvegarde ajean.link

Plan d'implémentation de référence. À lire avant d'écrire du code. Deux
nouveautés qui partagent la même fondation cryptographique :

1. **Chiffrer la mémoire** (les pages `memory/*.md`), en option, réversible,
   pour abonnés comme non-abonnés.
2. **Sauvegarder** la mémoire, les presets et tous les réglages sur ajean.link,
   de façon que le relais ne voie jamais rien (auto-périodique + manuel).

La règle qui prime sur tout le reste :

> **On ne détruit jamais une donnée tant que son remplaçant n'a pas été relu et
> vérifié octet pour octet. En cas de doute, on garde et on alerte, on n'écrase
> jamais.** La perte de mémoire est le scénario catastrophe absolu.

---

## 1. Contraintes de départ (pourquoi ce design et pas un autre)

- **Autonomie 100%.** L'agent doit pouvoir générer à tout moment, y compris sans
  navigateur connecté. Conséquence mathématique : la clé de déchiffrement doit
  être disponible côté serveur quand l'agent tourne. Le seul instant où elle ne
  l'est pas : juste après un redémarrage à froid, avant qu'un client se soit
  reconnecté une fois. Accepté (un client repasse quasi toujours très vite).
- **Confiance uniquement dans l'app + le mot de passe utilisateur.** Pas de
  dépendance au TPM ni au keystore de l'OS.
- **Marche sans ajean link.** Le secret ne peut donc PAS être la racine E2E `R`
  (qui n'existe que sur le chemin relais/abonné). En accès local/LAN, l'auth est
  un simple Bearer `web_key` côté serveur, sans secret dérivé du mot de passe.
- **Le relais est hostile par hypothèse.** Il peut être piraté. Il ne doit
  jamais voir un octet de contenu, de nom de fichier, ni de clé.
- **Jamais de perte de mémoire à cause d'un bug.** Redondance, atomicité,
  vérification avant suppression, clé de récupération, snapshots.

---

## 2. Architecture : chiffrement à enveloppe (DEK + KEK)

Deux clés, jamais une seule. C'est ce qui rend la rotation de mot de passe
triviale et le multi-appareils possible.

- **DEK** (Data Encryption Key) : 32 octets aléatoires, générée une seule fois à
  l'activation. Chiffre chaque page mémoire en AES-256-GCM (un nonce aléatoire
  par écriture). **Ne change jamais** tant que le chiffrement est actif.
- **KEK** (Key Encryption Key) : dérivée **côté client** d'un secret utilisateur
  (mot de passe mémoire) par **Argon2id**. Sert uniquement à (dé)wrapper la DEK.

```
mot de passe ──Argon2id──▶ KEK ──(dé)wrap──▶ DEK ──AES-256-GCM──▶ memory/*.md.enc
                            │                  │
                            │                  └──▶ bundle chiffré ──▶ relais (opaque)
                            └── keyvault : DEK wrappée × N (appareils + récupération + R abonné)
```

Points clés :
- La **DEK en clair n'existe jamais sur disque**. Sur disque : les pages
  chiffrées + le keyvault (DEK wrappée).
- Le **déballage se fait côté client** : le navigateur récupère le keyvault,
  dérive la KEK localement (le mot de passe ne quitte jamais le client), déballe
  la DEK, et l'envoie à l'agent par le canal sécurisé (scellée E2E pour les
  abonnés, via HTTPS/LAN en local). L'agent tient la DEK **en RAM** tant qu'il
  tourne. Autonomie assurée entre deux redémarrages.

---

## 3. Format sur disque

> Note d'implémentation (écart assumé au plan initial) : le NOM de fichier ne
> change pas (toujours `foo.md`). C'est le CONTENU qui est chiffré en place,
> précédé d'un magic `AJEANMEMv1` qui rend chaque fichier auto-descriptif. Ça
> évite de toucher au listing, à la recherche et à la validation de chemins, et
> la détection clair/chiffré se fait sur le contenu, pas sur l'extension. Aussi
> sûr, moins de surface de bug. Le reste de cette section décrit l'intention ;
> le code (mem_crypto.go, mem_io.go) fait foi.

Sous `$AJEAN_HOME/memory/` :

- `*.md` : une page. Si chiffrée : `magic || nonce || AES-256-GCM(contenu)`. Le
  nom reste en clair (nécessaire pour lister/chercher sans déverrouiller). Le
  contenu, lui, est opaque. Un `*.md.bak` garde la version précédente.
- `.keyvault` : JSON versionné.
  ```jsonc
  {
    "v": 1,
    "kdf": { "algo": "argon2id", "salt": "…", "t": 3, "m": 65536, "p": 1 },
    "check": "…",              // valeur témoin chiffrée par la DEK, pour valider un unlock
    "wraps": [                 // chaque wrap suffit seul à récupérer la DEK
      { "kind": "password", "label": "principal", "nonce": "…", "ct": "…" },
      { "kind": "recovery", "label": "clé de récupération", "nonce": "…", "ct": "…" },
      { "kind": "e2e-root",  "label": "mot de passe app", "nonce": "…", "ct": "…" }
    ]
  }
  ```
- `.manifest` : hash (BLAKE2/SHA-256) par page, pour détecter une page manquante,
  tronquée ou altérée au-delà de ce que le tag GCM couvre déjà.
- `.migration` : journal éphémère présent uniquement pendant une bascule
  (voir §5), pour reprendre ou annuler proprement après un crash.

Redondance du keyvault (point de défaillance unique) : **trois copies
indépendantes** tenues synchronisées, chacune suffisante seule :
`memory/.keyvault`, une copie dans `$AJEAN_HOME/`, une dans `ajean.db`.

Réglage : clé `MEM_ENCRYPTED` dans la config (comme `MEM_MODE`).

---

## 4. Cycle de vie

### Déverrouillage (à chaque session, réarme la DEK en RAM)
1. Le client récupère `.keyvault`.
2. Il dérive la KEK (Argon2id) à partir du mot de passe, **en local**.
3. Il déballe la DEK, valide contre `check`.
4. Il transmet la DEK à l'agent (scellée E2E, ou HTTPS/LAN).
5. L'agent garde la DEK en RAM jusqu'au prochain redémarrage.

### Écriture courante par l'IA (writeMem)
Atomique et non destructive : écrire dans un fichier temporaire, `fsync`,
**relire et vérifier le round-trip**, garder un `.bak` de la version précédente,
puis `rename`. Un crash en plein milieu ne corrompt jamais une page.

### Rotation du mot de passe (ou de la clé UI)
On ne re-chiffre **jamais** les pages. On déballe la DEK avec l'ancienne KEK, on
la re-wrappe avec la nouvelle, on remplace **seulement** le wrap concerné dans le
keyvault. Instantané, quel que soit le nombre de pages.

### Multi-appareils / révocation
Le keyvault contient plusieurs wraps de la même DEK (un par appareil ou mot de
passe). Ajouter un appareil = ajouter un wrap. Révoquer = retirer son wrap.

---

## 5. Durabilité (garantir qu'on ne perd JAMAIS la mémoire)

### Auto-test avant d'armer
Avant d'activer le chiffrement sur cette machine/ce build : round-trip de test
sur un échantillon. Si la crypto échoue, on **refuse d'activer** et on ne touche
pas à la mémoire.

### Activation (chiffrer) — jamais un point de non-retour
1. **Snapshot complet** de `memory/` mis de côté avant de commencer.
2. Écrire `*.md.enc` **à côté** des `*.md`.
3. Pour chaque page : relire le `.enc`, déchiffrer, **comparer octet pour octet**
   au `.md` d'origine.
4. Seulement si TOUTES les pages passent : retirer les `.md`.
5. La moindre anomalie ⇒ on abandonne, les `.md` en clair sont intacts.

### Désactivation (déchiffrer)
Symétrique : écrire les `.md`, vérifier le round-trip, puis seulement retirer les
`.enc`.

### Journal de migration
Un fichier `.migration` décrit l'opération en cours. Si le process coupe au
milieu, au redémarrage on détecte l'état incomplet et on **reprend ou on annule
proprement**. Jamais d'état bâtard.

### Snapshots locaux versionnés (filet pour tous, abonnés ou non)
Copies automatiques des N derniers états de `memory/` (ciphertext + keyvault)
sous `$AJEAN_HOME/backups/`. Un bug qui corromprait les fichiers courants se
rejoue en un clic depuis un snapshot antérieur, sans être abonné.

### Détection et refus de nuire
- Manifeste de hashs : détection immédiate d'une page manquante/altérée.
- Page qui ne déchiffre pas : **jamais supprimée**, signalée fort dans l'UI,
  restauration proposée (snapshot local, puis relais).
- Keyvault illisible au démarrage : l'agent **refuse d'écrire** en mémoire plutôt
  que d'aggraver. Fail-safe, pas fail-destructif.

### Écran de santé mémoire (UI)
En un coup d'œil : nombre de pages, toutes déchiffrables ou non, copies du
keyvault présentes, date du dernier snapshot local, date de la dernière
sauvegarde relais, clé de récupération confirmée.

---

## 6. Sauvegarde ajean.link (relais aveugle)

Le relais ne stocke que de l'opaque, par construction.

### Format du blob
- Empaqueter `{memory/*.md.enc, presets/*, config, keyvault}` en un tar.
- Chiffrer le tar entier en AES-256-GCM avec une **DEK-de-sauvegarde**.
- En-tête du blob : DEK-de-sauvegarde **wrappée par la KEK** (mot de passe) +
  wrap par la clé de récupération. Corps : le tar chiffré.
- Comme le tar est dans le corps chiffré, **même les noms de fichiers et de
  presets sont cachés**.

### Ce que reçoit le relais
Un blob opaque + le `link_token` (rattachement au compte) + métadonnées
strictement techniques (taille, date, version de format). **Aucun contenu, aucun
nom, aucune clé.**

### Endpoints côté `jean-relay` (aveugles)
- `PUT  /backup`        : dépose un blob (auth par token d'abonnement).
- `GET  /backup`        : liste les versions (id, taille, date).
- `GET  /backup/{id}`   : télécharge un blob.
- `DELETE /backup/{id}` : purge (rotation des versions).
Quota et rotation par compte (garder les M dernières versions).

### Restauration (serveur vierge)
Télécharger le blob, l'utilisateur fournit son mot de passe (ou la clé de
récupération), la KEK déballe la DEK-de-sauvegarde, on déchiffre le tar, on
restaure. Le nouveau serveur n'a besoin de rien d'autre.

### Déclenchement
Auto-périodique (quotidien et/ou sur changement significatif) + bouton
« Sauvegarder maintenant ». Restauration manuelle depuis l'UI.

---

## 7. Fichiers touchés (côté Go)

Nouveau :
- `internal/ajean/mem_crypto.go` : DEK/KEK, Argon2id, wrap/unwrap, AES-GCM,
  auto-test, format `.md.enc` et `.keyvault`.
- `internal/ajean/mem_vault.go` : keyvault (3 copies synchronisées, wraps,
  clé de récupération, valeur témoin, révocation).
- `internal/ajean/mem_migrate.go` : activation/désactivation vérifiées +
  journal `.migration` + reprise.
- `internal/ajean/mem_snapshots.go` : snapshots locaux versionnés + restauration.
- `internal/ajean/backup_bundle.go` : construction/restauration du blob.
- `internal/ajean/relay_backup.go` : client des endpoints relais.
- Côté `jean-relay` (autre dépôt) : stockage de blobs par compte.

Modifié :
- `internal/ajean/chat_memory.go` : `MemList`/`MemContent`/`MemSave`/`MemRead`/
  `MemSearch`/`MemAdd`/`MemEdit`/`MemDelete` passent par une couche transparente
  `readMem`/`writeMem` (chiffre/déchiffre à la volée quand `MEM_ENCRYPTED`).
- `internal/ajean/web_api.go` : endpoints déverrouillage (réception DEK scellée),
  activation/désactivation, santé mémoire, déclenchement/restauration sauvegarde.
- `internal/ajean/relay_e2e.go` / `relay_e2eauth.go` : réutiliser le canal scellé
  pour transporter la DEK au déverrouillage (abonnés).
- UI (`ui/src/`) : mot de passe mémoire, toggle chiffrer/déchiffrer, case
  « réutiliser mon mot de passe app », affichage/confirmation de la clé de
  récupération, écran de santé, boutons sauvegarde/restauration.

---

## 8. Ordre de livraison (par phases indépendantes et sûres)

1. **Fondation crypto sans rien casser** : `mem_crypto.go` + `mem_vault.go` +
   auto-test, couche `readMem`/`writeMem` inerte tant que `MEM_ENCRYPTED` est
   faux. Tests unitaires exhaustifs (round-trip, rotation, wraps multiples,
   corruption détectée).
2. **Snapshots locaux** (`mem_snapshots.go`) : filet de sécurité AVANT toute
   bascule. Indépendant du chiffrement, utile tout seul.
3. **Migration vérifiée** (`mem_migrate.go`) : activation/désactivation avec
   snapshot préalable, vérification octet pour octet, journal, reprise.
4. **UI chiffrement** : toggle, mot de passe mémoire, clé de récupération, écran
   de santé.
5. **Déverrouillage E2E/LAN** : transport de la DEK, DEK en RAM, réarmement à la
   reconnexion.
6. **Sauvegarde relais** : bundle + endpoints aveugles + auto/manuel +
   restauration.

Chaque phase est livrable seule et laisse la mémoire dans un état sûr.

---

## 9. Tests (non négociables vu l'enjeu)

- Round-trip chiffre/déchiffre sur milliers de pages, comparaison octet pour
  octet.
- Rotation de mot de passe : pages inchangées, seul le wrap change.
- Récupération via clé de récupération quand le wrap-mot-de-passe est détruit.
- Crash simulé à chaque étape de la migration : jamais de perte, reprise correcte.
- Corruption injectée (bit-flip, fichier tronqué, page manquante) : détectée,
  jamais silencieuse, restauration proposée.
- Keyvault : perte d'une copie sur trois ⇒ récupération depuis une autre.
- Sauvegarde relais : le relais ne voit que de l'opaque (vérifié par test) ;
  restauration sur environnement vierge à partir du seul mot de passe.
- Non-abonné : chiffrement + snapshots + restauration locale sans relais.

---

## 10. Décisions déjà prises

- Autonomie prioritaire ; verrouillage seulement au démarrage à froid avant
  première reconnexion (accepté).
- Enveloppe DEK/KEK, secret = mot de passe mémoire (pas le TPM, pas l'OS).
- Universel abonné/non-abonné.
- **Clé de récupération obligatoire** (filet anti-perte).
- Sauvegarde relais totalement aveugle, auto-périodique + manuel.
