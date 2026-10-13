# go-capi-client

A Go HTTP service that exposes a REST API for viewing and managing
[Cluster API](https://cluster-api.sigs.k8s.io/) resources in a Kubernetes
namespace.

The service can use either a local `kubeconfig` or a pod's ServiceAccount. It
also provides an OpenAPI specification and a Swagger UI or Stoplight Elements
interface.

> [!WARNING]
> This project is currently a prototype. The authentication middleware allows
> every request. Cluster creation requires a configured `cluster-defaults` ConfigMap. Do not expose the service
> publicly as-is.

## Features

- health checks through `GET /ping`;
- create, read, list, and delete CAPI `Cluster` resources;
- read a cluster kubeconfig from a Kubernetes `Secret`;
- interactive OpenAPI documentation with Swagger UI or Stoplight;
- Kubernetes access using a `kubeconfig` or in-cluster configuration;
- non-root Docker image;
- Kubernetes deployment through a Helm chart with namespace-scoped RBAC,
  probes, and optional Ingress and autoscaling.

## HTTP errors

Request validation now returns **422 Unprocessable Entity** instead of 400.
Clients that handle validation failures by status must accept 422.

| Status | Meaning |
|---|---|
| 400 | Request body cannot be decoded: malformed JSON, incompatible field type, or unknown field |
| 422 | Missing required field, invalid IP, replica count below 1, or invalid tag name/value |
| 404 | Cluster or tag not found |
| 409 | Cluster already exists |
| 500 | Internal failure or missing server configuration |

Errors use the existing Fuego response format (`status`, `title`, `detail`,
and optional `errors` for individual fields). For example, a missing cluster
name returns:

```json
{
  "status": 422,
  "title": "Validation Error",
  "detail": "Name is required",
  "errors": [
    {
      "name": "ClusterCreateParams.Name",
      "reason": "Key: 'ClusterCreateParams.Name' Error:Field validation for 'Name' failed on the 'required' tag",
      "more": {
        "nsField": "ClusterCreateParams.Name",
        "field": "Name",
        "tag": "required",
        "param": "",
        "value": ""
      }
    }
  ]
}
```

An empty cluster creation body also returns 422 because its required fields
are absent. Empty tag values remain allowed. Validation failures returned
directly by the Kubernetes API are not covered by this mapping.

## Requirements

- Go `1.26.5`, or a version compatible with the `go.mod` file;
- access to a Kubernetes cluster;
- the Cluster API CRDs, including `clusters.cluster.x-k8s.io`;
- permissions on `clusters`, `machines`, and `secrets` in the target namespace;
- optionally, Docker to build the image;
- optionally, Helm 3 to install the chart;
- optionally, `pre-commit` and `golangci-lint` for local quality checks.

## Development

The Git hooks require `pre-commit` and `golangci-lint` to be available in your
`PATH`. On Debian or Ubuntu, install `pre-commit` in an isolated environment
with `pipx`:

```bash
sudo apt update
sudo apt install pipx
pipx ensurepath
pipx install pre-commit
```

Open a new terminal after `pipx ensurepath`, then check that the required
commands are available:

```bash
pre-commit --version
golangci-lint --version
```

See the
[`golangci-lint` installation guide](https://golangci-lint.run/docs/welcome/install/local/)
if the second command is missing.

After cloning the repository, install both the pre-commit and pre-push Git
hooks:

```bash
make pre-commit-install
```

The pre-commit hook checks common file issues and runs `golangci-lint` on Go
changes. The pre-push hook runs the Go test suite. Run all pre-commit checks
manually with:

```bash
make pre-commit
```

## Quick start

### From a local workstation

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

Check that the service is running:

```bash
curl http://127.0.0.1:8080/ping
# {"message":"pong"}
```

The OpenAPI interface is available under `/swagger/`, and the JSON
specification under `/swagger/openapi.json`.

### From a Kubernetes pod

```bash
go run ./cmd/http_server \
  --kube-client=in-cluster \
  --namespace=capi-system \
  --address=0.0.0.0 \
  --port=8080 \
  --swagger-ui=stoplight
```

In `in-cluster` mode, the client automatically uses the token and certificate
authority mounted in the pod.

## Server configuration

All options are exposed as Cobra flags:

| Flag | Default | Description |
| --- | --- | --- |
| `--kube-client` | `kubeconfig` | Kubernetes configuration source: `kubeconfig` or `in-cluster` |
| `--kubeconfig` | `$HOME/.kube/config` | Absolute kubeconfig path, used in `kubeconfig` mode |
| `--namespace`, `-n` | `default` | Namespace containing the Cluster API resources |
| `--address` | `0.0.0.0` | HTTP server listen address |
| `--port`, `-p` | `8080` | HTTP server listen port |
| `--swagger-ui` | `stoplight` | OpenAPI interface: `swagger`, `stoplight`, or `disabled` |

To display the help generated by the binary:

```bash
go run ./cmd/http_server --help
```

With `--swagger-ui=disabled`, the user interface is disabled, but the
specification remains available under `/swagger/openapi.json`.

## HTTP API

Base URL: `http://localhost:8080`.

| Method | Route | Description | Current status |
| --- | --- | --- | --- |
| `GET` | `/ping` | Checks service health | Working |
| `GET` | `/kubernetes/clusters/` | Lists clusters, or selected cluster fields, in the namespace | Working |
| `GET` | `/kubernetes/clusters/{id}` | Returns one cluster | Working |
| `POST` | `/kubernetes/clusters/` | Creates a CAPI cluster and its associated resources | Requires configured defaults |
| `DELETE` | `/kubernetes/clusters/{id}` | Deletes a cluster | Working |
| `GET` | `/kubernetes/clusters/{id}/config` | Reads the associated kubeconfig | Convention-based |
| `GET` | `/kubernetes/clusters/{id}/nodes` | Returns the cluster nodes, or selected node fields, from CAPI Machines | Working |

### Examples

List clusters:

```bash
curl http://127.0.0.1:8080/kubernetes/clusters/
```

Example response:

```json
[
  {
    "name": "demo",
    "phase": "Provisioned"
  }
]
```
Use the `fields` query parameter to choose the response representation. An
absent, empty value or `*` returns every field.
Otherwise, provide comma-separated JSON field names. Dot-separated paths select
nested fields. Unknown or malformed fields will be ignored.

Return full cluster objects:

```bash
curl 'http://127.0.0.1:8080/kubernetes/clusters/?fields=*'
```

Return only the cluster name and the ready replica count:

```bash
curl 'http://127.0.0.1:8080/kubernetes/clusters/?fields=name,control_plane_status.ready_replicas'
```

The nodes collection uses the same selection syntax.

Create a cluster:

```bash
curl --request POST http://127.0.0.1:8080/kubernetes/clusters/ \
  --header 'Content-Type: application/json' \
  --data '{
    "name": "my-cluster",
    "control_plane_replicas": 3,
    "worker_replicas": 2,
    "control_plane_ip": "10.30.138.14",
    "vm_name_prefix": "jls-capi-dev-",
    "description": "Development cluster for the backend team"
  }'
```

The HTTP API supplies the cluster name, replica counts, control-plane IPv4
address, VM name prefix, description and tags. The `cluster-defaults` ConfigMap in
`--namespace` is read on each creation; changes affect new clusters only.

The optional `description` accepts up to 4,000 Unicode code points, including
quotes, accents, emojis and line breaks (escaped as required by JSON). Longer
descriptions return HTTP 422. It is stored on the CAPI `Cluster` in the
`vmk.vates.tech/description` annotation and returned in creation, detail and list
responses. Empty descriptions and clusters without this annotation omit the
response field.

This example reproduces the test cluster configuration. If `my-cluster`
already exists, creation returns 409. To create another cluster alongside it,
use a different name, an unused control-plane IP and a distinct VM name prefix.

Cluster creation creates a Cluster without `spec.topology`, an XOCluster, a
KubeadmControlPlane, one MachineDeployment, two per-cluster XOMachineTemplates,
and a per-cluster KubeadmConfigTemplate. The Cluster network comes from the
ConfigMap. The endpoint is configured on XOCluster and propagated by the provider.
The requested control-plane IP is also added to the API server certificate SANs.

Required ConfigMap keys:

| Key | Purpose |
| --- | --- |
| `kubernetesVersion` | Version for control plane and workers |
| `workerName` | Worker deployment suffix, e.g. `md-0` |
| `podCIDR`, `serviceCIDR`, `serviceDomain` | Cluster network |
| `controlPlaneEndpointPort`, `controlPlaneEndpointSubnet` | Endpoint port and IPv4 prefix length |
| `controlPlaneLoadBalancer` | `kube-vip` or an explicitly empty string |
| `cni` | Provider CNI setting, e.g. `cilium` or `none` |
| `controlPlaneMachineSpec`, `workerMachineSpec` | YAML objects containing the respective XOMachine spec: image, pool, networks, resources |
| `controlPlaneKubeadmConfig`, `workerKubeadmConfig` | YAML objects containing the respective kubeadm configuration and bootstrap commands |

No Kubernetes templates need to be installed beforehand. The service builds
all resources from the request and ConfigMap, including the per-cluster machine
and worker bootstrap templates required by CAPI. It does not create or read a
KubeadmControlPlaneTemplate. Each YAML block contains configuration fields only,
without `apiVersion`, `kind`, `metadata` or a surrounding `spec`.

The chart defaults use the test cluster's XO VM image, pool and network
identifiers, with kubeadm, cloud-init and kube-vip enabled in both machine specs.
Before deploying in another environment, update these identifiers. These are Xen
Orchestra identifiers, not Kubernetes resource names. The provider also accepts
`templateName` and `poolName`; omitting the pool uses the template's pool.
The service decodes these configuration objects and leaves provider field
validation to Kubernetes and the Vates provider. The sample kubeadm
configuration follows the working AlmaLinux/RHEL cluster: it installs the exact
`kubernetesVersion`, containerd and runc, disables swap, configures kernel modules
and sysctls, and enables the services. It requires a compatible dnf-based VM image
and access to the package repositories. Adapt these commands for other images.
Within `preKubeadmCommands`, the service replaces only `{{KUBERNETES_VERSION}}`
and `{{CONTROL_PLANE_IP}}` with the configured version and requested endpoint.
Shell variables such as `$FULL`, `$IP` and `$(hostname)` remain intact.
Credentials remain in provider-managed Secrets (the provider's global credentials
are used by this configuration).

The service sets VM name prefixes, replicas, Kubernetes version and resource
references from the request/defaults, and adds the endpoint IP to certificate
SANs. Other configured VM/bootstrap fields are preserved. MachineHealthChecks
are not created. The ConfigMap is decoded and all resources are built before
the first write. Generated resources belong to the Cluster. XOCluster additionally marks this
owner as its controller for compatibility with older Vates providers using
`GetControllerOf`; other resource ownership is unchanged. Cilium (or the configured
CNI), CCM and CSI are explicitly requested on XOCluster. The Cluster stays paused
until creation succeeds. On partial failure, delete the cluster through the HTTP
API and wait for cleanup before retrying. An activation timeout can leave the
cluster active: inspect its state before retrying.

When upgrading from the earlier configuration, replace the four template-name
keys with the four YAML blocks above and deploy the new service image. Updating
only the binary or only the ConfigMap is insufficient. The supplied Helm values
and `k8s/deployment.yaml` include the new structure.

Cluster tags are stored as labels on the CAPI `Cluster` resource under the
`tags.vmk.vates.tech/` prefix. For example, the tag `environment=production`
becomes `tags.vmk.vates.tech/environment=production`. Tag names and values
must follow Kubernetes label syntax and are limited to 63 characters by the
[Kubernetes label specification](https://kubernetes.io/docs/concepts/overview/working-with-objects/labels/#syntax-and-character-set).
Tags may be supplied in the cluster creation request or managed later through
`GET /clusters/{id}/tags`, `GET /clusters/{id}/tags/{tag}`,
`PUT /clusters/{id}/tags/{tag}` with `{"value":"production"}`, and
`DELETE /clusters/{id}/tags/{tag}` under `/kubernetes`. The cluster labels
are the persistent source of truth; tags previously kept only in process
memory do not migrate across a deployment.

The HTTP server delegates these operations to `ClusterManager`, which owns
the tag use cases and uses `ClusterClient` for Kubernetes label persistence.
The client enforces the constraints specific to Kubernetes labels. Tags are also
included in cluster creation, detail, and list responses.

`GET /kubernetes/tags` returns all tags grouped by cluster name in the
configured namespace, including clusters without tags:

```json
{"result":{"demo":{"environment":"production"},"no-tags":{}}}
```

When there are no clusters, the response is `{"result":{}}`.

View a cluster and its nodes:

```bash
curl http://127.0.0.1:8080/kubernetes/clusters/demo
curl http://127.0.0.1:8080/kubernetes/clusters/demo/nodes
```

Each node entry includes a `machine_id` field when the CAPI `Machine` exposes a
Xen Orchestra `providerID` (for example `xenorchestra://<pool>/<vm-uuid>`). The
server extracts the VM UUID and exposes it as the machine identifier; if the
provider ID is missing or not from Xen Orchestra, the field is omitted.

This lets callers correlate a Kubernetes `Machine` with the corresponding Xen
Orchestra VM without exposing provider strings in the API response.

Retrieve its kubeconfig:

```bash
curl http://127.0.0.1:8080/kubernetes/clusters/demo/config
```

The service looks for the `demo-kubeconfig` `Secret` in the configured
namespace and returns the value stored under its `value` key in a JSON object:

```json
{"kubeconfig":"apiVersion: v1\nkind: Config"}
```

Delete the cluster:

```bash
curl --request DELETE http://127.0.0.1:8080/kubernetes/clusters/demo
```

Successful cluster and tag deletions, as well as tag creation and updates, return
`204 No Content` with an empty body.

## Docker image

Build the image:

```bash
docker build --tag go-capi-client:local .
```

Run the image with a read-only local kubeconfig mount:

```bash
docker run --rm --publish 8080:8080 \
  --volume "$HOME/.kube/config:/config/kubeconfig:ro" \
  go-capi-client:local \
  --kube-client=kubeconfig \
  --kubeconfig=/config/kubeconfig \
  --namespace=default
```

The final image is based on Alpine, listens on port `8080`, and runs as the
unprivileged user `10001`.

## Helm installation

Install the service in the namespace containing the Cluster API resources:

```bash
helm upgrade --install go-capi-client ./charts/go-capi-client \
  --namespace capi-system \
  --create-namespace \
  --set image.repository=registry.example.com/go-capi-client \
  --set image.tag=0.1.0
```

If the Cluster API resources are in a namespace other than the Helm release
namespace, set `config.namespace`:

```bash
helm upgrade --install go-capi-client ./charts/go-capi-client \
  --namespace services \
  --create-namespace \
  --set config.namespace=capi-system \
  --set image.repository=registry.example.com/go-capi-client \
  --set image.tag=0.1.0
```

By default, the chart creates a `Role` in the target namespace and a
`RoleBinding` for the release ServiceAccount. Disable this behavior with
`--set rbac.create=false` when permissions are managed separately.

### Main Helm values

| Value | Default | Description |
| --- | --- | --- |
| `replicaCount` | `1` | Replica count when autoscaling is disabled |
| `image.repository` | `go-capi-client` | Image repository |
| `image.tag` | chart `appVersion` | Image tag |
| `image.pullPolicy` | `IfNotPresent` | Image pull policy |
| `config.namespace` | release namespace | Target CAPI namespace |
| `config.address` | `0.0.0.0` | Listen address |
| `config.port` | `8080` | Container port |
| `config.swaggerUI` | `stoplight` | OpenAPI interface |
| `service.type` | `ClusterIP` | Service type |
| `service.port` | `80` | Service port |
| `rbac.create` | `true` | Create the Role and RoleBinding |
| `serviceAccount.create` | `true` | Create the ServiceAccount |
| `ingress.enabled` | `false` | Create an Ingress |
| `autoscaling.enabled` | `false` | Create an HPA |

See [`charts/go-capi-client/values.yaml`](charts/go-capi-client/values.yaml) for
all available values.

### Local access to the Helm service

```bash
kubectl --namespace capi-system port-forward service/go-capi-client 8080:80
curl http://127.0.0.1:8080/ping
```

## Required RBAC permissions

The service uses the following minimum permissions in the target namespace:

| API group | Resources | Verbs |
| --- | --- | --- |
| `cluster.x-k8s.io` | `clusters` | `get`, `list`, `create`, `delete`, `patch` |
| `cluster.x-k8s.io` | `machines` | `list` |
| core group | `secrets`, `configmaps` | `get` |
| `cluster.x-k8s.io` | `machinedeployments` | `create` |
| `vates.infrastructure.cluster.x-k8s.io` | `xoclusters`, `xomachinetemplates` | `create` |
| `controlplane.cluster.x-k8s.io` | `kubeadmcontrolplanes` | `create` |
| `bootstrap.cluster.x-k8s.io` | `kubeadmconfigtemplates` | `create` |

Secret access allows the service to read kubeconfigs. Treat the ServiceAccount
and API access as sensitive.

## Development

Build the server:

```bash
go build ./cmd/http_server
```

Run the available tests:

```bash
make unit
```

The unit suite currently covers the `ClusterManager`. Unit tests use the
`unit` build tag, which is applied by the Make target.

Regenerate GoMock implementations after changing a Strategy interface:

```bash
make mocks
```

Run static analysis and vulnerability scanning when the required tools are
installed:

```bash
make lint   # requires golangci-lint
make vuln   # requires govulncheck
```

## Architecture

```text
HTTP client
    |
    v
HTTP component (Fuego routes and handlers)
    |
    v  ForManagingClusters
ClusterManager
    |
    +-- ForManagingClusterResources --> ClusterClient --> CAPI Cluster resources
    +-- ForGettingClusterConfig     --> SecretClient  --> Kubernetes Secrets
    `-- ForGettingClusterMachines   --> MachineClient --> CAPI Machine resources
```

`cmd/http_server/main.go` is the Configurator: it creates the Kubernetes
Strategies, injects them into `ClusterManager`, then injects `ClusterManager`
into the HTTP component. This follows Alistair Cockburn's **Component +
Strategy** pattern. No Adapter is needed here because the Kubernetes clients
directly satisfy the required interfaces.

See [architecture.md](architecture.md) for the pattern's mapping to the code,
runtime request flow, dependency rules, and current architectural gaps.

Main repository structure:

```text
cmd/http_server/                 entry point and CLI configuration
pkg/components/webserver/   HTTP routes, handlers, validation, and DTOs
pkg/components/cluster_manager/ cluster-management orchestration
pkg/components/kubernetes_clients/ Kubernetes Strategy implementations
pkg/strategies/             shared contracts and cross-contract models
charts/go-capi-client/           Helm chart
k8s/deployment.yaml              basic Kubernetes manifest for local use
Dockerfile                       multi-stage image build
```

## Known limitations and roadmap

- implement proper HTTP authentication and authorization;
- enrich `/nodes` Machine data with information from Kubernetes Node resources;
- add server-side defaults and policy for cluster creation;
- map Kubernetes errors to explicit HTTP status codes;
- extend unit coverage and add integration and end-to-end tests;
- add observability, structured logging, and graceful shutdown;
- define an image versioning and publishing strategy.

## License

This repository does not currently include a license file. Add one before
distributing the project publicly.
