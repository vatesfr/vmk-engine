# go-capi-client

Service HTTP écrit en Go qui expose une API REST pour consulter et gérer des
ressources [Cluster API](https://cluster-api.sigs.k8s.io/) dans un namespace
Kubernetes.

Le service peut utiliser un `kubeconfig` local ou le ServiceAccount du pod. Il
fournit également une spécification OpenAPI et une interface Swagger UI ou
Stoplight Elements.

> [!WARNING]
> Le projet est actuellement un prototype. Le middleware d'authentification
> laisse passer toutes les requêtes, l'endpoint des nœuds renvoie des données
> simulées et la création ne produit qu'une ressource CAPI `Cluster` minimale.
> Ne l'exposez pas publiquement en l'état.

## Fonctionnalités

- vérification de santé avec `GET /ping` ;
- création, lecture, liste et suppression de ressources CAPI `Cluster` ;
- lecture du kubeconfig d'un cluster depuis un `Secret` Kubernetes ;
- inspection OpenAPI interactive avec Swagger UI ou Stoplight ;
- accès Kubernetes par `kubeconfig` ou configuration in-cluster ;
- image Docker non-root ;
- déploiement Kubernetes via un chart Helm avec RBAC limité au namespace cible,
  probes, Ingress et autoscaling optionnels.

## Prérequis

- Go `1.26.5` ou une version compatible avec le fichier `go.mod` ;
- un cluster Kubernetes accessible ;
- les CRD Cluster API installées, notamment `clusters.cluster.x-k8s.io` ;
- des droits sur les ressources `clusters`, `machines` et `secrets` du namespace
  utilisé ;
- facultativement Docker pour construire l'image ;
- facultativement Helm 3 pour installer le chart.

## Démarrage rapide

### Depuis un poste local

```bash
go mod download
go run ./cmd/http_server \
  --kube-client=kubeconfig \
  --kubeconfig="$HOME/.kube/config" \
  --namespace=default \
  --address=127.0.0.1 \
  --port=8080 \
  --swagger-ui=swagger
```

Vérifiez ensuite que le service répond :

```bash
curl http://127.0.0.1:8080/ping
# {"message":"pong"}
```

L'interface OpenAPI est disponible sous `/swagger/` et la spécification JSON
sous `/swagger/openapi.json`.

### Depuis un pod Kubernetes

```bash
go run ./cmd/http_server \
  --kube-client=in-cluster \
  --namespace=capi-system \
  --address=0.0.0.0 \
  --port=8080 \
  --swagger-ui=stoplight
```

En mode `in-cluster`, le client utilise automatiquement le token et l'autorité
de certification montés dans le pod.

## Configuration du serveur

Toutes les options sont des flags Cobra :

| Flag | Valeur par défaut | Description |
| --- | --- | --- |
| `--kube-client` | `kubeconfig` | Source de configuration Kubernetes : `kubeconfig` ou `in-cluster` |
| `--kubeconfig` | `$HOME/.kube/config` | Chemin absolu du kubeconfig, utilisé en mode `kubeconfig` |
| `--namespace`, `-n` | `default` | Namespace contenant les ressources Cluster API |
| `--address` | `0.0.0.0` | Adresse d'écoute du serveur HTTP |
| `--port`, `-p` | `8080` | Port d'écoute du serveur HTTP |
| `--swagger-ui` | `stoplight` | Interface OpenAPI : `swagger`, `stoplight` ou `disabled` |

Pour afficher l'aide générée par le binaire :

```bash
go run ./cmd/http_server --help
```

Avec `--swagger-ui=disabled`, l'interface est désactivée mais la spécification
reste accessible sous `/swagger/openapi.json`.

## API HTTP

URL de base : `http://localhost:8080`.

| Méthode | Route | Description | État actuel |
| --- | --- | --- | --- |
| `GET` | `/ping` | Vérifie la santé du service | Fonctionnel |
| `GET` | `/api/kubernetes/clusters/` | Liste les clusters du namespace | Fonctionnel |
| `GET` | `/api/kubernetes/clusters/{id}` | Retourne un cluster | Fonctionnel |
| `POST` | `/api/kubernetes/clusters/` | Crée un objet CAPI `Cluster` minimal | Fonctionnel, limité |
| `DELETE` | `/api/kubernetes/clusters/{id}` | Supprime un cluster | Fonctionnel |
| `GET` | `/api/kubernetes/clusters/{id}/config` | Lit le kubeconfig associé | Fonctionnel sous convention |
| `GET` | `/api/kubernetes/clusters/{id}/nodes` | Retourne les nœuds du cluster | Données simulées |

### Exemples

Lister les clusters :

```bash
curl http://127.0.0.1:8080/api/kubernetes/clusters/
```

Réponse type :

```json
[
  {
    "name": "demo",
    "phase": "Provisioned"
  }
]
```

Créer un cluster :

```bash
curl --request POST http://127.0.0.1:8080/api/kubernetes/clusters/ \
  --header 'Content-Type: application/json' \
  --data '{"name":"demo"}'
```

Cette opération crée uniquement un objet `Cluster` avec son nom et son
namespace. Elle ne crée pas l'infrastructure, le control plane, les
`MachineDeployment` ou leurs templates.

Consulter un cluster et ses nœuds :

```bash
curl http://127.0.0.1:8080/api/kubernetes/clusters/demo
curl http://127.0.0.1:8080/api/kubernetes/clusters/demo/nodes
```

Récupérer son kubeconfig :

```bash
curl http://127.0.0.1:8080/api/kubernetes/clusters/demo/config
```

Le service cherche le `Secret` `demo-kubeconfig` dans le namespace configuré et
retourne la valeur de sa clé `value`.

Supprimer le cluster :

```bash
curl --request DELETE http://127.0.0.1:8080/api/kubernetes/clusters/demo
```

## Image Docker

Construire l'image :

```bash
docker build --tag go-capi-client:local .
```

Lancer l'image avec un kubeconfig local monté en lecture seule :

```bash
docker run --rm --publish 8080:8080 \
  --volume "$HOME/.kube/config:/config/kubeconfig:ro" \
  go-capi-client:local \
  --kube-client=kubeconfig \
  --kubeconfig=/config/kubeconfig \
  --namespace=default
```

L'image finale est basée sur Alpine, écoute sur le port `8080` et s'exécute avec
l'utilisateur non privilégié `10001`.

## Installation avec Helm

Installez le service dans le namespace qui contient les ressources Cluster API :

```bash
helm upgrade --install go-capi-client ./charts/go-capi-client \
  --namespace capi-system \
  --create-namespace \
  --set image.repository=registry.example.com/go-capi-client \
  --set image.tag=0.1.0
```

Si les ressources Cluster API se trouvent dans un autre namespace que la
release Helm, définissez `config.namespace` :

```bash
helm upgrade --install go-capi-client ./charts/go-capi-client \
  --namespace services \
  --create-namespace \
  --set config.namespace=capi-system \
  --set image.repository=registry.example.com/go-capi-client \
  --set image.tag=0.1.0
```

Le chart crée par défaut un `Role` dans le namespace cible et un `RoleBinding`
vers le ServiceAccount de la release. Désactivez cette gestion avec
`--set rbac.create=false` si les permissions sont provisionnées séparément.

### Principales valeurs Helm

| Valeur | Défaut | Description |
| --- | --- | --- |
| `replicaCount` | `1` | Nombre de réplicas hors autoscaling |
| `image.repository` | `go-capi-client` | Dépôt de l'image |
| `image.tag` | `appVersion` du chart | Tag de l'image |
| `image.pullPolicy` | `IfNotPresent` | Politique de téléchargement |
| `config.namespace` | namespace de la release | Namespace CAPI cible |
| `config.address` | `0.0.0.0` | Adresse d'écoute |
| `config.port` | `8080` | Port du conteneur |
| `config.swaggerUI` | `stoplight` | Interface OpenAPI |
| `service.type` | `ClusterIP` | Type du Service |
| `service.port` | `80` | Port du Service |
| `rbac.create` | `true` | Création du Role et du RoleBinding |
| `serviceAccount.create` | `true` | Création du ServiceAccount |
| `ingress.enabled` | `false` | Création d'un Ingress |
| `autoscaling.enabled` | `false` | Création d'un HPA |

Consultez [`charts/go-capi-client/values.yaml`](charts/go-capi-client/values.yaml)
pour l'ensemble des valeurs disponibles.

### Accès local au service Helm

```bash
kubectl --namespace capi-system port-forward service/go-capi-client 8080:80
curl http://127.0.0.1:8080/ping
```

## RBAC requis

Le service utilise les permissions minimales suivantes dans le namespace cible :

| Groupe API | Ressources | Verbes |
| --- | --- | --- |
| `cluster.x-k8s.io` | `clusters` | `get`, `list`, `create`, `delete` |
| `cluster.x-k8s.io` | `machines` | `get` |
| groupe core | `secrets` | `get` |

L'accès aux Secrets permet de lire des kubeconfigs : traitez le compte de
service et l'accès à l'API comme des éléments sensibles.

## Développement

Compiler le serveur :

```bash
go build ./cmd/http_server
```

Exécuter les tests disponibles :

```bash
go test ./...
```

Lancer l'analyse statique et la recherche de vulnérabilités si les outils sont
installés :

```bash
make lint   # nécessite golangci-lint
make vuln   # nécessite govulncheck
```

Le dépôt ne contient actuellement aucun fichier de test automatisé.

## Architecture

```text
Client HTTP
    |
    v
Fuego (routes, OpenAPI, middleware)
    |
    v
K8sHTTPAdapter
    |
    +-- ClusterClient  --> ressources CAPI Cluster
    +-- MachineClient  --> ressources CAPI Machine
    `-- SecretClient   --> Secrets Kubernetes
```

Organisation principale du dépôt :

```text
cmd/http_server/                 point d'entrée et configuration CLI
internal/webserver/              routes, handlers, middleware et modèles HTTP
internal/adapters/               adaptation entre l'API HTTP et Kubernetes
internal/kubernetes_clients/     accès aux ressources Kubernetes/CAPI
charts/go-capi-client/           chart Helm
k8s/deployment.yaml              manifeste Kubernetes simple pour usage local
Dockerfile                       construction multi-stage de l'image
```

## Limites connues et feuille de route

- implémenter une authentification et une autorisation HTTP réelles ;
- remplacer les données simulées de `/nodes` par la résolution
  Machines/Nœuds ;
- prendre en charge une spécification de cluster complète à la création ;
- normaliser les erreurs Kubernetes en statuts HTTP explicites ;
- ajouter des tests unitaires, d'intégration et end-to-end ;
- ajouter observabilité, journalisation structurée et arrêt gracieux ;
- définir une stratégie de versionnement et de publication d'images.

## Licence

Aucun fichier de licence n'est actuellement fourni dans ce dépôt. Ajoutez une
licence avant toute distribution publique du projet.
