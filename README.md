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

Cluster list and detail responses include `is_available`, which is `true` only
when the CAPI Cluster `Available` condition is `True`. Missing, `False`, or
`Unknown` conditions yield `false`. This field can also be selected with
`GET /kubernetes/clusters?fields=name,is_available`.

## HTTP errors

Request validation now returns **422 Unprocessable Entity** instead of 400.
Clients that handle validation failures by status must accept 422.

| Status | Meaning |
|---|---|
| 400 | Request body cannot be decoded: malformed JSON, incompatible field type, or unknown field |
| 422 | Missing required field, invalid cluster name, invalid IP, replica count below 1, or invalid tag name/value |
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

## Pause and resume CAPI reconciliation

Pause controls CAPI reconciliation; it does not stop VMs or running workloads.
The namespace is the one configured for the server.

```sh
# Pause and resume a cluster.
curl --request POST http://127.0.0.1:8080/kubernetes/clusters/demo/reconciliation/pause
curl --request POST http://127.0.0.1:8080/kubernetes/clusters/demo/reconciliation/resume

# Pause and resume a Machine using its CAPI metadata.name (not its Node name or VM UUID).
curl --request POST http://127.0.0.1:8080/kubernetes/clusters/demo/nodes/demo-worker-abc/reconciliation/pause
curl --request POST http://127.0.0.1:8080/kubernetes/clusters/demo/nodes/demo-worker-abc/reconciliation/resume
```

These endpoints take no body and return `204 No Content` after the requested
state is saved. Cluster pause sets `spec.paused: true`; resume removes that field.
Machine pause sets `cluster.x-k8s.io/paused: ""`, overwriting any previous value;
resume removes only that annotation. Other fields and annotations are preserved.
Repeated requests for the same state succeed. A missing cluster or Machine, or a
Machine belonging to another cluster, returns `404`; invalid parameters return
`422`. Concurrent modification returns `409`; read the resource again and retry.

Cluster and node responses include `is_reconciliation_paused`, including when it is `false`, and
collection endpoints support `?fields=name,is_reconciliation_paused`. For clusters this reflects
`spec.paused`; for nodes it reflects the Machine's own pause annotation, regardless
of its value. A Machine without its own annotation may still be affected by its
parent cluster's pause. Resuming a cluster preserves individual Machine pauses;
resuming a Machine preserves the parent cluster's pause. A Cluster pause annotation
set externally is also preserved: these endpoints control only `spec.paused`.

Deleting a paused cluster retains the existing behavior: deletion is requested
first, then its `spec.paused` field is cleared to allow controller cleanup.

## HTTP API

Base URL: `http://localhost:8080`.

| Method | Route | Description | Current status |
| --- | --- | --- | --- |
| `GET` | `/ping` | Checks service health | Working |
| `GET` | `/kubernetes/clusters` | Lists clusters, or selected cluster fields, in the namespace | Working |
| `GET` | `/kubernetes/clusters/{id}` | Returns one cluster | Working |
| `POST` | `/kubernetes/clusters` | Creates a CAPI cluster and its associated resources | Requires an available version and configured defaults |
| `DELETE` | `/kubernetes/clusters/{id}` | Deletes a cluster | Working |
| `PUT` | `/kubernetes/clusters/{id}/workers/replicas` | Sets the desired worker count | Working |
| `PUT` | `/kubernetes/clusters/{id}/control-plane/replicas` | Sets the desired control-plane count | Working |
| `DELETE` | `/kubernetes/clusters/{id}/nodes/{name}` | Requests deletion of a node belonging to the cluster | Working |
| `POST` | `/kubernetes/clusters/{id}/reconciliation/pause` | Pauses cluster reconciliation | Idempotent, returns `204` |
| `POST` | `/kubernetes/clusters/{id}/reconciliation/resume` | Resumes cluster reconciliation | Idempotent, returns `204` |
| `POST` | `/kubernetes/clusters/{id}/nodes/{nodeName}/reconciliation/pause` | Pauses a CAPI Machine | Idempotent, returns `204` |
| `POST` | `/kubernetes/clusters/{id}/nodes/{nodeName}/reconciliation/resume` | Removes a CAPI Machine pause | Idempotent, returns `204` |
| `GET` | `/kubernetes/clusters/{id}/config` | Reads the associated kubeconfig | Convention-based |
| `GET` | `/kubernetes/clusters/{id}/nodes` | Returns the cluster nodes, or selected node fields, from CAPI Machines | Working |
| `GET` | `/kubernetes/nodes` | Returns all CAPI Machines in the namespace | Working |
| `GET` | `/kubernetes/versions-available` | Lists available Kubernetes releases grouped by minor version, newest first | Static catalog |
| `GET` | `/kubernetes/tags` | Lists tags grouped by cluster name | Working |
| `GET` | `/kubernetes/clusters/{id}/tags` | Lists cluster tags | Working |
| `GET` | `/kubernetes/clusters/{id}/tags/{tag}` | Reads a tag | Working |
| `PUT` | `/kubernetes/clusters/{id}/tags/{tag}` | Creates or updates a tag | Working |
| `DELETE` | `/kubernetes/clusters/{id}/tags/{tag}` | Deletes a tag | Working |

### Examples

List clusters:

```bash
curl http://127.0.0.1:8080/kubernetes/clusters
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
curl 'http://127.0.0.1:8080/kubernetes/clusters?fields=*'
```

Return only the cluster name and the ready replica count:

```bash
curl 'http://127.0.0.1:8080/kubernetes/clusters?fields=name,control_plane_status.ready_replicas'
```

The nodes collection uses the same selection syntax.

List available Kubernetes versions before creating a cluster:

```bash
curl http://127.0.0.1:8080/kubernetes/versions-available
```

The response is an ordered array of objects with `version` (the minor version)
and `releases` (the full versions). Both groups and releases are ordered newest
first. Choose a full version from `releases`, such as `1.36.9`, for the creation
payload. The catalog is defined in `pkg/components/version_manager`; it is not
fetched from Kubernetes or an external release service.

Create a cluster:

```bash
curl --request POST http://127.0.0.1:8080/kubernetes/clusters \
  --header 'Content-Type: application/json' \
  --data '{
    "name": "my-cluster",
    "control_plane_replicas": 3,
    "worker_replicas": 2,
    "kubernetes_version": "1.36.9",
    "control_plane_ip": "10.30.138.14",
    "vm_name_prefix": "jls-capi-dev-",
    "description": "Development cluster for the backend team"
  }'
```

The HTTP API supplies the cluster name, replica counts, Kubernetes version,
control-plane IPv4 address, VM name prefix, description and tags.
The cluster name must be a lowercase RFC 1123 label: lowercase letters, digits
or hyphens, starting and ending with a letter or digit. Invalid names return
HTTP 422 with validation details. Generated resource names (including the
`-cp`, `-worker` and worker deployment suffixes) must fit within 63 characters.
The `cluster-defaults` ConfigMap in `--namespace` is read on each creation;
changes affect new clusters only.

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
requested `kubernetes_version`, containerd and runc, disables swap, configures kernel modules
and sysctls, and enables the services. It requires a compatible dnf-based VM image
and access to the package repositories. Adapt these commands for other images.
Within `preKubeadmCommands`, the service replaces only `{{KUBERNETES_VERSION}}`
and `{{CONTROL_PLANE_IP}}` with the requested version and endpoint.
Shell variables such as `$FULL`, `$IP` and `$(hostname)` remain intact.
Credentials remain in provider-managed Secrets (the provider's global credentials
are used by this configuration).

The creation payload requires `kubernetes_version`, selected from `GET /kubernetes/versions-available`
(a full `major.minor.patch` release, for example `1.36.9`, without the `v` prefix). Missing or unavailable
versions return HTTP 422 before reading defaults or creating resources. The
version is no longer read from the ConfigMap. An existing `kubernetesVersion`
key is ignored and can be removed. The service adds the `v` prefix internally
when building CAPI resources and rendering bootstrap commands.

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

The `name` field always contains the CAPI resource name, the stable identifier
used by the delete endpoint. The optional `nodeName` field contains
`status.nodeRef.name`, the registered Kubernetes Node name; it is omitted until
the Node reference is available. `name` remains the resource identifier before
and after registration; `nodeName` is descriptive and is not used for deletion.

Delete a node using its `name` from the nodes response:

```bash
curl --request DELETE \
  http://127.0.0.1:8080/kubernetes/clusters/demo/nodes/demo-worker-abcde
```

A successful request returns `204 No Content` after Kubernetes accepts deletion.
CAPI controllers perform cleanup asynchronously, including their usual drain
and infrastructure cleanup; the endpoint does not bypass finalizers. The
configured namespace and the Machine's `spec.clusterName` restrict the target.
A missing cluster, missing node, or node belonging to another cluster
returns `404`; an empty cluster or node name at the service boundary returns
`422`; a concurrent change detected by deletion preconditions returns `409`
(the client can retry); unexpected Kubernetes errors return `500`.

This operation deletes an individual node without changing the desired
replica count. Its owning controller can create a replacement. To permanently
reduce cluster capacity, use the replicas endpoints below.

### Changing replica counts

Set the number of workers:

```bash
curl --request PUT \
  http://127.0.0.1:8080/kubernetes/clusters/demo/workers/replicas \
  --header 'Content-Type: application/json' \
  --data '{"replicas":3}'
```

Set the number of control-plane machines independently:

```bash
curl --request PUT \
  http://127.0.0.1:8080/kubernetes/clusters/demo/control-plane/replicas \
  --header 'Content-Type: application/json' \
  --data '{"replicas":3}'
```

Both endpoints require an integer `replicas` between 1 and 2147483647. Zero
workers are not supported. With stacked etcd, the control-plane count must be
odd; external-etcd control planes may use an even count. The service reads
etcd configuration from the existing KubeadmControlPlane, not creation defaults.

A successful call returns `204 No Content` once Kubernetes confirms the desired
count was written, including when that count was already set. CAPI reconciles
machines asynchronously and performs its normal drain and cleanup on scale-down.
The service changes only `spec.replicas`; it does not select individual machines
for deletion. Use `GET /kubernetes/clusters/demo` and
`GET /kubernetes/clusters/demo/nodes` to follow progress. Cluster status, including
`desired_replicas`, may temporarily lag behind the accepted request.

This first version supports clusters without `spec.topology`, with exactly one
worker MachineDeployment and a KubeadmControlPlane. Workers are discovered in the
configured namespace using the cluster label and verified against `spec.clusterName`;
the control plane is resolved through `spec.controlPlaneRef`, without assuming
resource name suffixes. Cluster owner references, when present, must match the
current Cluster UID. Worker deployments with either CAPI autoscaler size
annotation cannot be scaled through these endpoints.

| Code | Meaning |
| --- | --- |
| `400` | Malformed JSON, incompatible type, integer overflow, or unknown field |
| `404` | Cluster not found |
| `409` | Cluster/target paused or deleting, unsupported topology/provider, absent or ambiguous target, autoscaler management, ownership mismatch, or concurrent target change |
| `422` | Missing/null replicas (including an empty body), count below 1, invalid stacked-etcd parity, or Kubernetes admission rejecting the count |
| `500` | Unexpected Kubernetes or service configuration error |

Patches include the target's `resourceVersion`. A concurrent change returns
`409` without automatic retries. Read the current state before submitting the
same desired count again. A timeout can leave the write outcome uncertain;
inspect the state before retrying. Each route performs at most one write, so
there is no cross-resource rollback. The Cluster and target state checks are
reads, not a transaction across resources: another actor can change the Cluster
or add a worker group between validation and the target patch.

The routes use the existing HTTP middleware; `AuthRequired` currently forwards
requests without enforcing authentication. RBAC permissions must be upgraded
along with the service (see Required RBAC permissions below).

Each node entry includes a `node_id` field when the CAPI `Machine` exposes a
Xen Orchestra `providerID` (for example `xenorchestra://<pool>/<vm-uuid>`). The
server extracts the VM UUID and exposes it as `node_id`; if the
provider ID is missing or not from Xen Orchestra, the field is omitted.

This lets callers correlate a node with the corresponding Xen
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
| `cluster.x-k8s.io` | `machines` | `get`, `list`, `delete`, `patch` |
| core group | `secrets`, `configmaps` | `get` |
| `cluster.x-k8s.io` | `machinedeployments` | `create`, `get`, `list`, `patch` |
| `vates.infrastructure.cluster.x-k8s.io` | `xoclusters`, `xomachinetemplates` | `create` |
| `controlplane.cluster.x-k8s.io` | `kubeadmcontrolplanes` | `create`, `get`, `patch` |
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

The suite covers the cluster and version managers, Kubernetes clients using
fake clients, handlers, and HTTP routes using `httptest`. Tests also run with
`go test ./...`; the Make target additionally supplies the `unit` build tag.

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
    +-- ForManagingVersions --------> VersionManager (static catalog)
    |
    v  ForManagingClusters
ClusterManager
    |
    +-- ForManagingClusterResources --> ClusterClient --> CAPI Cluster resources
    +-- ForGettingClusterConfig     --> SecretClient  --> Kubernetes Secrets
    `-- ForManagingClusterMachines   --> MachineClient --> CAPI Machine resources
    +-- ForGettingClusterCreationDefaults --> ConfigMapClient --> cluster-defaults
    `-- ForManagingVersions         --> VersionManager (same instance)
```

`cmd/http_server/main.go` is the Configurator: it creates the Kubernetes
Strategies, injects them into `ClusterManager`, then injects `ClusterManager`
and the shared `VersionManager` into the HTTP component. `ClusterManager`
validates the requested version through `ForManagingVersions` before reading
creation defaults. This follows Alistair Cockburn's **Component +
Strategy** pattern. No Adapter is needed here because the Kubernetes clients
directly satisfy the required interfaces.

See [architecture.md](architecture.md) for the pattern's mapping to the code,
runtime request flow, dependency rules, and current architectural gaps.

Main repository structure:

```text
cmd/http_server/                 entry point and CLI configuration
pkg/components/webserver/   HTTP routes, handlers, validation, and DTOs
pkg/components/cluster_manager/ cluster-management orchestration
pkg/components/version_manager/ available Kubernetes releases and validation
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
- extend the existing HTTP error mapping for additional Kubernetes failure cases;
- extend unit coverage and add integration and end-to-end tests;
- add observability, structured logging, and graceful shutdown;
- define an image versioning and publishing strategy.

## License

This repository does not currently include a license file. Add one before
distributing the project publicly.
