# Parallel Search MCP

[English](#english) · [Français](#français)

## English

Connect AJEAN to [Parallel Search MCP](https://docs.parallel.ai/integrations/mcp/search-mcp)
for web search and page extraction over Streamable HTTP. The anonymous endpoint
is free for light use and needs no Parallel account or API key. Rate limits apply;
running your model is separate. Search queries and fetched URLs are sent to
Parallel, so this optional service makes requests outside your machine.

### Add the server

Install AJEAN using the [installation guide](../README.md#install), or
[build it from source](../README.md#build-from-source). In the interface on the
machine running AJEAN, enable **agent mode**, open **MCP servers**, and add:

| Field | Value |
|---|---|
| Name | `parallel-search` |
| Transport | HTTP |
| URL | `https://search.parallel.ai/mcp` |
| Headers (one per line) | `User-Agent: ajean (https://github.com/nathaninline/ajean)` |

Save, then check that the server is connected and both `web_search` and
`web_fetch` are enabled. No `Authorization` header is needed. MCP settings are
managed locally; they cannot be changed through the remote relay.

Alternatively, from this checkout, send the supplied
[API payload](parallel-search-mcp.json) to a running local AJEAN interface:

```bash
curl --fail-with-body http://localhost:8090/api/mcp/save \
  -H 'Content-Type: application/json' \
  --data-binary @docs/parallel-search-mcp.json
```

If you protected the control API, also pass `-H "Authorization: Bearer $AJEAN_WEB_KEY"`
with your AJEAN control key. That key authenticates the local control request,
not the Parallel server. The JSON is a request body for `/api/mcp/save`, not a
file AJEAN reads automatically. It adds a server named `parallel-search` and
leaves other servers and the built-in web engine unchanged; saving with an
existing server name replaces that server's configuration.

### Use and disable

In Project mode with agent mode on and a model that supports tool calling, ask
the model to use `mcp__parallel-search__web_search` to find information, then
`mcp__parallel-search__web_fetch` to read a result. For example:

> Use Parallel search to find the official Go release notes, then fetch the
> official page and summarize it with a source link.

The search tool takes an `objective` and a nonempty `search_queries` array;
the fetch tool takes a `urls` array. AJEAN discovers the current schemas from
the server and exposes the tools with the names above. The built-in `web_search`
remains available if Internet is enabled, so specify Parallel when you want
this server. Tool selection depends on your model.

Use the server switch in **MCP servers** to disconnect it, or switch off either
individual tool. If the server reports a rate limit, wait before trying again;
the free tier is not unlimited. See the [Parallel documentation](https://docs.parallel.ai/integrations/mcp/search-mcp)
for current limits and tool parameters.

## Français

Connectez AJEAN à [Parallel Search MCP](https://docs.parallel.ai/integrations/mcp/search-mcp)
pour la recherche web et l’extraction de pages en Streamable HTTP. Le point
d’accès anonyme est gratuit pour un usage léger, sans compte ni clé API
Parallel. Des limites de requêtes s’appliquent ; l’exécution du modèle reste
distincte. Les recherches et les URL à extraire sont envoyées à Parallel : ce
service facultatif effectue donc des requêtes hors de votre machine.

### Ajouter le serveur

Installez AJEAN avec le [guide d’installation](../README.fr.md#installation), ou
[compilez les sources](../README.fr.md#compiler-depuis-les-sources). Dans l’interface
sur la machine qui exécute AJEAN, activez le **mode agent**, ouvrez
**Serveurs MCP**, puis ajoutez :

| Champ | Valeur |
|---|---|
| Nom | `parallel-search` |
| Transport | HTTP |
| URL | `https://search.parallel.ai/mcp` |
| En-têtes (un par ligne) | `User-Agent: ajean (https://github.com/nathaninline/ajean)` |

Enregistrez, puis vérifiez que le serveur est connecté et que `web_search` et
`web_fetch` sont activés. Aucun en-tête `Authorization` n’est nécessaire. La
configuration MCP se gère localement, pas via le relais distant.

Vous pouvez aussi envoyer le [corps de requête fourni](parallel-search-mcp.json)
depuis ce dépôt à une interface AJEAN locale en cours d’exécution :

```bash
curl --fail-with-body http://localhost:8090/api/mcp/save \
  -H 'Content-Type: application/json' \
  --data-binary @docs/parallel-search-mcp.json
```

Si l’API de contrôle est protégée, ajoutez
`-H "Authorization: Bearer $AJEAN_WEB_KEY"` avec votre clé de contrôle AJEAN.
Elle authentifie la requête locale, pas le serveur Parallel. Ce JSON est le
corps d’une requête `/api/mcp/save`, pas un fichier chargé automatiquement par
AJEAN. Il ajoute le serveur `parallel-search` sans modifier les autres serveurs
ni le moteur web intégré ; un nom déjà utilisé remplace la configuration de ce
serveur.

### Utiliser et désactiver

En mode Projet, avec le mode agent activé et un modèle capable d’appeler des
outils, demandez à l’IA d’utiliser `mcp__parallel-search__web_search` pour
chercher, puis `mcp__parallel-search__web_fetch` pour lire un résultat. Exemple :

> Utilise la recherche Parallel pour trouver les notes de version officielles
> de Go, puis extrais la page officielle et résume-la avec un lien vers la source.

La recherche prend un `objective` et un tableau `search_queries` non vide ;
l’extraction prend un tableau `urls`. AJEAN découvre les schémas actuels auprès
du serveur et expose les outils sous les noms ci-dessus. Le `web_search`
intégré reste disponible si Internet est activé : précisez Parallel lorsque
vous souhaitez ce serveur. Le choix de l’outil dépend de votre modèle.

L’interrupteur du serveur dans **Serveurs MCP** permet de le déconnecter ;
chaque outil peut aussi être désactivé séparément. En cas de limite de requêtes,
attendez avant de réessayer : l’offre gratuite n’est pas illimitée. Consultez
la [documentation Parallel](https://docs.parallel.ai/integrations/mcp/search-mcp)
pour les limites et les paramètres actuels.
