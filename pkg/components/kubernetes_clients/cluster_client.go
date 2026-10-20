package kubernetes_clients

import (
	"context"
	"fmt"
	"net/netip"
	"strings"

	"github.com/gofrs/uuid"
	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"capv-proxy/pkg/strategies"
	"capv-proxy/pkg/uuids"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/utils/ptr"
)

type ClusterClient struct {
	kubernetesClient client.Client
	informerCache    cache.Cache
	namespace        string
}

const (
	scalingClusterKind      = "Cluster"
	xoGroup                 = "vates.infrastructure.cluster.x-k8s.io"
	cpGroup                 = "controlplane.cluster.x-k8s.io"
	bootstrapGroup          = "bootstrap.cluster.x-k8s.io"
	specKey                 = "spec"
	templateKey             = "template"
	metadataKey             = "metadata"
	nameKey                 = "name"
	namePrefixKey           = "namePrefix"
	kubeadmConfigSpecKey    = "kubeadmConfigSpec"
	clusterConfigurationKey = "clusterConfiguration"
	apiServerKey            = "apiServer"
	certSANsKey             = "certSANs"
	xoClusterKind           = "XOCluster"
	xoMachineTemplateKind   = "XOMachineTemplate"
	controlPlaneKind        = "KubeadmControlPlane"
	bootstrapTemplateKind   = "KubeadmConfigTemplate"
	endpointKey             = "controlPlaneEndpoint"
	bootstrapKey            = "bootstrap"
	commandsKey             = "preKubeadmCommands"
	kindKey                 = "kind"
	clusterTagLabelPrefix   = "tags.vmk.vates.tech/"
)

const clusterDescriptionAnnotation = "vmk.vates.tech/description"

var _ strategies.ForManagingClusterResources = ClusterClient{}

func NewClusterClient(kubernetesClient client.Client, informerCache cache.Cache, namespace string) ClusterClient {
	return ClusterClient{kubernetesClient: kubernetesClient, informerCache: informerCache, namespace: namespace}
}

func (c ClusterClient) Create(ctx context.Context, params strategies.CreateClusterResourceParams) (strategies.ClusterInfo, error) {
	if params.Name == "" {
		return strategies.ClusterInfo{}, strategies.ErrClusterNameRequired
	}
	if c.namespace == "" {
		return strategies.ClusterInfo{}, strategies.ErrClusterNamespaceRequired
	}

	labels, err := toClusterTagLabels(params.Tags)
	if err != nil {
		return strategies.ClusterInfo{}, err
	}

	return c.createResources(ctx, params, labels)
}

func (c ClusterClient) List(ctx context.Context) ([]strategies.ClusterInfo, error) {
	clusters := &capiv1.ClusterList{}
	if err := c.informerCache.List(
		ctx,
		clusters,
		client.InNamespace(c.namespace),
	); err != nil {
		return nil, err
	}

	result := make([]strategies.ClusterInfo, 0, len(clusters.Items))
	for i := range clusters.Items {
		result = append(result, toInfo(&clusters.Items[i]))
	}
	return result, nil
}

func (c ClusterClient) Get(ctx context.Context, name string) (strategies.ClusterInfo, error) {
	clusterResource := &capiv1.Cluster{}
	if err := c.informerCache.Get(
		ctx,
		client.ObjectKey{Namespace: c.namespace, Name: name},
		clusterResource,
	); err != nil {
		if apierrors.IsNotFound(err) {
			return strategies.ClusterInfo{}, fmt.Errorf("%w: %w", strategies.ErrClusterNotFound, err)
		}
		return strategies.ClusterInfo{}, err
	}

	return toInfo(clusterResource), nil
}

func (c ClusterClient) GetByUUID(ctx context.Context, id uuid.UUID) (strategies.ClusterInfo, error) {
	list := &capiv1.ClusterList{}
	if err := c.informerCache.List(ctx, list,
		client.InNamespace(c.namespace),
		client.MatchingFields{uuids.UUIDIndexKey: id.String()},
	); err != nil {
		return strategies.ClusterInfo{}, fmt.Errorf("%w: %w", strategies.ErrClusterRetrievalFailed, err)
	}
	if len(list.Items) == 0 {
		return strategies.ClusterInfo{}, fmt.Errorf("%w: %s", strategies.ErrClusterNotFound, id.String())
	}
	return toInfo(&list.Items[0]), nil
}

func (c ClusterClient) Delete(ctx context.Context, name string) error {
	if name == "" {
		return strategies.ErrClusterNameRequired
	}
	if c.namespace == "" {
		return strategies.ErrClusterNamespaceRequired
	}

	cluster := &capiv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: c.namespace,
		},
	}

	if err := c.kubernetesClient.Delete(ctx, cluster); err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("%w: %w", strategies.ErrClusterNotFound, err)
		}
		return fmt.Errorf("delete Cluster %s/%s: %w", c.namespace, name, err)
	}

	// A partial creation may still be paused. Unpause only after
	// deletion is requested, so controllers reconcile cleanup, not provisioning.
	if err := c.kubernetesClient.Get(ctx, client.ObjectKeyFromObject(cluster), cluster); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("read deleting Cluster %s: %w", name, err)
	}
	if ptr.Deref(cluster.Spec.Paused, false) && !cluster.DeletionTimestamp.IsZero() {
		before := cluster.DeepCopy()
		cluster.Spec.Paused = nil
		if err := c.kubernetesClient.Patch(ctx, cluster, client.MergeFrom(before)); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("unpause deleting Cluster %s: %w", name, err)
		}
	}

	return nil
}

// GetAllTags returns tags keyed by cluster name in the configured namespace.
// Clusters without tags are included with an empty map.
func (c ClusterClient) GetAllTags(ctx context.Context) (map[string]map[string]string, error) {
	clusters := &capiv1.ClusterList{}
	if err := c.informerCache.List(ctx, clusters, client.InNamespace(c.namespace)); err != nil {
		return nil, err
	}

	tags := make(map[string]map[string]string, len(clusters.Items))
	for i := range clusters.Items {
		cluster := &clusters.Items[i]
		tags[cluster.Name] = clusterTagsFromLabels(cluster.Labels)
	}
	return tags, nil
}

func (c ClusterClient) GetTags(ctx context.Context, clusterName string) (map[string]string, error) {
	cluster, err := c.getClusterFromCache(ctx, clusterName)
	if err != nil {
		return nil, err
	}
	return clusterTagsFromLabels(cluster.Labels), nil
}

func (c ClusterClient) GetTag(ctx context.Context, clusterName, tag string) (string, error) {
	if err := validateClusterTag(tag, ""); err != nil {
		return "", err
	}
	cluster, err := c.getClusterFromCache(ctx, clusterName)
	if err != nil {
		return "", err
	}
	value, found := cluster.Labels[clusterTagLabelPrefix+tag]
	if !found {
		return "", strategies.ErrClusterTagNotFound
	}
	return value, nil
}

func (c ClusterClient) SetTag(ctx context.Context, clusterName, tag, value string) error {
	if err := validateClusterTag(tag, value); err != nil {
		return err
	}
	return c.updateClusterTags(ctx, clusterName, func(labels map[string]string) error {
		labels[clusterTagLabelPrefix+tag] = value
		return nil
	})
}

func (c ClusterClient) DeleteTag(ctx context.Context, clusterName, tag string) error {
	if err := validateClusterTag(tag, ""); err != nil {
		return err
	}
	return c.updateClusterTags(ctx, clusterName, func(labels map[string]string) error {
		labelName := clusterTagLabelPrefix + tag
		if _, found := labels[labelName]; !found {
			return strategies.ErrClusterTagNotFound
		}
		delete(labels, labelName)
		return nil
	})
}

func toInfo(value *capiv1.Cluster) strategies.ClusterInfo {
	controlPlaneEndpoint := ""
	if value.Spec.ControlPlaneEndpoint.IsValid() {
		controlPlaneEndpoint = "https://" + value.Spec.ControlPlaneEndpoint.String()
	}

	info := strategies.ClusterInfo{
		Paused:                      ptr.Deref(value.Spec.Paused, false),
		IsAvailable:                 apimeta.IsStatusConditionTrue(value.Status.Conditions, capiv1.ClusterAvailableCondition),
		ID:                          uuids.ClusterID(value.GetName()),
		IsControlPlaneInitialized:   value.Status.Initialization.ControlPlaneInitialized,
		IsInfrastructureProvisioned: value.Status.Initialization.InfrastructureProvisioned,
		Name:                        value.GetName(),
		Description:                 value.Annotations[clusterDescriptionAnnotation],
		Phase:                       value.Status.Phase,
		ControlPlaneEndpoint:        controlPlaneEndpoint,
		Version:                     value.Spec.Topology.Version,
		Tags:                        optionalClusterTags(value.Labels),
	}
	if info.Version == "" && value.Status.ControlPlane != nil && len(value.Status.ControlPlane.Versions) == 1 {
		info.Version = value.Status.ControlPlane.Versions[0].Version
	}
	info.CreatedAt = value.CreationTimestamp.String()
	if status := value.Status.ControlPlane; status != nil {
		info.ControlPlanesStatus = strategies.NodeTypeStatus{
			AvailableReplicas: int(ptr.Deref(status.AvailableReplicas, 0)),
			DesiredReplicas:   int(ptr.Deref(status.DesiredReplicas, 0)),
			ReadyReplicas:     int(ptr.Deref(status.ReadyReplicas, 0)),
			Replicas:          int(ptr.Deref(status.Replicas, 0)),
			UpToDateReplicas:  int(ptr.Deref(status.UpToDateReplicas, 0)),
			Versions:          toVersionNodeStatuses(status.Versions),
		}
	}
	if status := value.Status.Workers; status != nil {
		info.WorkersStatus = strategies.NodeTypeStatus{
			AvailableReplicas: int(ptr.Deref(status.AvailableReplicas, 0)),
			DesiredReplicas:   int(ptr.Deref(status.DesiredReplicas, 0)),
			ReadyReplicas:     int(ptr.Deref(status.ReadyReplicas, 0)),
			Replicas:          int(ptr.Deref(status.Replicas, 0)),
			UpToDateReplicas:  int(ptr.Deref(status.UpToDateReplicas, 0)),
			Versions:          toVersionNodeStatuses(status.Versions),
		}
	}
	return info
}

func toVersionNodeStatuses(versions []capiv1.StatusVersion) []strategies.VersionNodeStatus {
	var result []strategies.VersionNodeStatus
	for _, version := range versions {
		result = append(result, strategies.VersionNodeStatus{
			Replicas: int(version.Replicas),
			Version:  version.Version,
		})
	}
	return result
}

func toClusterTagLabels(tags map[string]string) (map[string]string, error) {
	if len(tags) == 0 {
		return nil, nil
	}
	labels := make(map[string]string, len(tags))
	for name, value := range tags {
		if err := validateClusterTag(name, value); err != nil {
			return nil, err
		}
		labels[clusterTagLabelPrefix+name] = value
	}
	return labels, nil
}

func validateClusterTag(name, value string) error {
	if name == "" {
		return strategies.ErrClusterTagNameRequired
	}
	// Kubernetes limits label names and values to 63 characters and restricts
	// their character set:
	// https://kubernetes.io/docs/concepts/overview/working-with-objects/labels/#syntax-and-character-set
	if strings.Contains(name, "/") || len(validation.IsQualifiedName(name)) != 0 {
		return fmt.Errorf("%w: %q", strategies.ErrInvalidClusterTagName, name)
	}
	if len(validation.IsValidLabelValue(value)) != 0 {
		return fmt.Errorf("%w: %q", strategies.ErrInvalidClusterTagValue, value)
	}
	return nil
}

func clusterTagsFromLabels(labels map[string]string) map[string]string {
	tags := make(map[string]string)
	for name, value := range labels {
		if strings.HasPrefix(name, clusterTagLabelPrefix) {
			tags[strings.TrimPrefix(name, clusterTagLabelPrefix)] = value
		}
	}
	return tags
}

func (c ClusterClient) updateClusterTags(ctx context.Context, clusterName string, update func(map[string]string) error) error {
	cluster, err := c.getClusterResource(ctx, clusterName)
	if err != nil {
		return err
	}
	original := cluster.DeepCopy()
	if cluster.Labels == nil {
		cluster.Labels = make(map[string]string)
	}
	if err := update(cluster.Labels); err != nil {
		return err
	}
	return c.kubernetesClient.Patch(ctx, cluster, client.MergeFrom(original))
}

// getClusterFromCache reads a Cluster from the informer cache (read-only operations).
func (c ClusterClient) getClusterFromCache(ctx context.Context, name string) (*capiv1.Cluster, error) {
	if name == "" {
		return nil, strategies.ErrClusterNameRequired
	}
	if c.namespace == "" {
		return nil, strategies.ErrClusterNamespaceRequired
	}
	cluster := &capiv1.Cluster{}
	if err := c.informerCache.Get(ctx, client.ObjectKey{Namespace: c.namespace, Name: name}, cluster); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("%w: %w", strategies.ErrClusterNotFound, err)
		}
		return nil, err
	}
	return cluster, nil
}

// getClusterResource reads a Cluster via the direct API client, needed before
// write operations (patch/update) where a fresh resourceVersion is required.
func (c ClusterClient) getClusterResource(ctx context.Context, name string) (*capiv1.Cluster, error) {
	if name == "" {
		return nil, strategies.ErrClusterNameRequired
	}
	if c.namespace == "" {
		return nil, strategies.ErrClusterNamespaceRequired
	}
	cluster := &capiv1.Cluster{}
	if err := c.kubernetesClient.Get(ctx, types.NamespacedName{Namespace: c.namespace, Name: name}, cluster); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("%w: %w", strategies.ErrClusterNotFound, err)
		}
		return nil, err
	}
	return cluster, nil
}

func optionalClusterTags(labels map[string]string) map[string]string {
	tags := clusterTagsFromLabels(labels)
	if len(tags) == 0 {
		return nil
	}
	return tags
}

func resource(group, kind, name, namespace string, spec map[string]any) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": group + "/v1beta2",
		kindKey:      kind,
		metadataKey: map[string]any{
			nameKey:     name,
			"namespace": namespace,
		},
		specKey: spec,
	}}
}

func reference(group, kind, name string) map[string]any {
	return map[string]any{"apiGroup": group, kindKey: kind, nameKey: name}
}

func (c ClusterClient) createResources(ctx context.Context, p strategies.CreateClusterResourceParams, labels map[string]string) (strategies.ClusterInfo, error) {
	d := p.Defaults
	ip, err := netip.ParseAddr(p.ControlPlaneIP)
	if err != nil || !ip.Is4() {
		return strategies.ClusterInfo{}, fmt.Errorf("controlPlaneIP must be an IPv4 address")
	}
	if p.ControlPlaneReplicas == nil || *p.ControlPlaneReplicas < 1 || len(p.MachineDeployments) != 1 || p.MachineDeployments[0].Replicas == nil || *p.MachineDeployments[0].Replicas < 1 {
		return strategies.ClusterInfo{}, fmt.Errorf("cluster creation requires control-plane replicas and one worker deployment")
	}
	worker := p.MachineDeployments[0]
	cpName, workerTemplateName, mdName := p.Name+"-cp", p.Name+"-worker", p.Name+"-"+worker.Name
	for _, name := range []string{p.Name, cpName, workerTemplateName} {
		if errs := validation.IsDNS1123Label(name); len(errs) > 0 {
			return strategies.ClusterInfo{}, fmt.Errorf("%w %q: generated resource name %q: %v", strategies.ErrInvalidClusterName, p.Name, name, errs)
		}
	}
	if errs := validation.IsDNS1123Label(mdName); len(errs) > 0 {
		// An invalid configured worker suffix remains a server configuration error.
		if len(validation.IsDNS1123Label(worker.Name)) == 0 {
			return strategies.ClusterInfo{}, fmt.Errorf("%w %q: generated resource name %q: %v", strategies.ErrInvalidClusterName, p.Name, mdName, errs)
		}
		return strategies.ClusterInfo{}, fmt.Errorf("invalid resource name %q: %v", mdName, errs)
	}
	if err := validateConfigurationObjects(d); err != nil {
		return strategies.ClusterInfo{}, err
	}
	cpSpec := map[string]any{kubeadmConfigSpecKey: runtime.DeepCopyJSON(d.ControlPlaneKubeadmConfig)}
	cpMachine := runtime.DeepCopyJSON(d.ControlPlaneMachineSpec)
	workerMachine := runtime.DeepCopyJSON(d.WorkerMachineSpec)
	bootstrap := runtime.DeepCopyJSON(d.WorkerKubeadmConfig)
	for _, config := range []map[string]any{cpSpec[kubeadmConfigSpecKey].(map[string]any), bootstrap} {
		if err := renderBootstrapCommands(config, p.Version, p.ControlPlaneIP); err != nil {
			return strategies.ClusterInfo{}, err
		}
	}
	prefix := p.VMNamePrefix
	if prefix == "" {
		prefix = p.Name
	}
	cpMachine[namePrefixKey] = prefix + "-cp"
	workerMachine[namePrefixKey] = prefix + "-worker"
	cpSpec["version"] = p.Version
	cpSpec["replicas"] = int64(*p.ControlPlaneReplicas)
	if err := unstructured.SetNestedMap(cpSpec, reference(xoGroup, xoMachineTemplateKind, cpName), "machineTemplate", specKey, "infrastructureRef"); err != nil {
		return strategies.ClusterInfo{}, err
	}
	sans, _, err := unstructured.NestedStringSlice(cpSpec, kubeadmConfigSpecKey, clusterConfigurationKey, apiServerKey, certSANsKey)
	if err != nil {
		return strategies.ClusterInfo{}, err
	}
	sans = append(sans, p.ControlPlaneIP)
	if err := unstructured.SetNestedStringSlice(cpSpec, sans, kubeadmConfigSpecKey, clusterConfigurationKey, apiServerKey, certSANsKey); err != nil {
		return strategies.ClusterInfo{}, err
	}
	if labels == nil {
		labels = map[string]string{}
	}
	labels[capiv1.ClusterNameLabel] = p.Name
	cluster := &capiv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: p.Name, Namespace: c.namespace, Labels: labels}, Spec: capiv1.ClusterSpec{
		Paused: ptr.To(true),
		ClusterNetwork: capiv1.ClusterNetwork{
			Pods:          capiv1.NetworkRanges{CIDRBlocks: []string{d.PodCIDR}},
			Services:      capiv1.NetworkRanges{CIDRBlocks: []string{d.ServiceCIDR}},
			ServiceDomain: d.ServiceDomain,
		},
		ControlPlaneRef:   capiv1.ContractVersionedObjectReference{APIGroup: cpGroup, Kind: controlPlaneKind, Name: cpName},
		InfrastructureRef: capiv1.ContractVersionedObjectReference{APIGroup: xoGroup, Kind: xoClusterKind, Name: p.Name},
	}}

	if p.Description != "" {
		cluster.Annotations = map[string]string{clusterDescriptionAnnotation: p.Description}
	}

	xoSpec := map[string]any{
		endpointKey:     map[string]any{"host": p.ControlPlaneIP, "port": int64(d.EndpointPort), "subnet": int64(d.EndpointSubnet)},
		"injectSSHKeys": false,
		"addons":        map[string]any{cniKey: d.CNI, "ccm": true, "csi": true},
	}
	if d.LoadBalancer != "" {
		xoSpec["controlPlaneLB"] = d.LoadBalancer
	}
	deploymentLabels := map[string]any{"cluster.x-k8s.io/deployment-name": mdName}
	workerSpec := map[string]any{
		"clusterName":       p.Name,
		"version":           p.Version,
		bootstrapKey:        map[string]any{"configRef": reference(bootstrapGroup, bootstrapTemplateKind, workerTemplateName)},
		"infrastructureRef": reference(xoGroup, xoMachineTemplateKind, workerTemplateName),
	}
	deploymentSpec := map[string]any{
		"clusterName": p.Name,
		"replicas":    int64(*worker.Replicas),
		"selector":    map[string]any{"matchLabels": deploymentLabels},
		templateKey:   map[string]any{metadataKey: map[string]any{"labels": deploymentLabels}, specKey: workerSpec},
	}
	objects := []*unstructured.Unstructured{
		resource(xoGroup, xoMachineTemplateKind, cpName, c.namespace, map[string]any{templateKey: map[string]any{specKey: cpMachine}}),
		resource(xoGroup, xoMachineTemplateKind, workerTemplateName, c.namespace, map[string]any{templateKey: map[string]any{specKey: workerMachine}}),
		resource(bootstrapGroup, bootstrapTemplateKind, workerTemplateName, c.namespace, map[string]any{templateKey: map[string]any{specKey: bootstrap}}),
		resource(xoGroup, xoClusterKind, p.Name, c.namespace, xoSpec),
		resource(cpGroup, controlPlaneKind, cpName, c.namespace, cpSpec),
		resource("cluster.x-k8s.io", "MachineDeployment", mdName, c.namespace, deploymentSpec),
	}

	if err := c.kubernetesClient.Create(ctx, cluster); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return strategies.ClusterInfo{}, fmt.Errorf("%w: %w", strategies.ErrClusterAlreadyExists, err)
		}
		return strategies.ClusterInfo{}, err
	}
	// Keep partial creations paused and identifiable for explicit cleanup. Never
	// delete resources after an ambiguous API timeout or adopt existing objects.
	for _, obj := range objects {
		obj.SetLabels(map[string]string{capiv1.ClusterNameLabel: p.Name})
		owner := metav1.OwnerReference{APIVersion: capiv1.GroupVersion.String(), Kind: "Cluster", Name: p.Name, UID: cluster.UID}
		// Older Vates controllers resolve the Cluster with GetControllerOf.
		// Restrict this compatibility reference to XOCluster; other resources keep
		// their normal CAPI ownership. No blockOwnerDeletion permission is needed.
		if obj.GetKind() == xoClusterKind {
			owner.Controller = ptr.To(true)
		}
		obj.SetOwnerReferences([]metav1.OwnerReference{owner})
		if err := c.kubernetesClient.Create(ctx, obj); err != nil {
			return strategies.ClusterInfo{}, fmt.Errorf("create %s %s: %w; Cluster %s remains paused; delete it before retrying", obj.GetKind(), obj.GetName(), err, p.Name)
		}
	}
	before := cluster.DeepCopy()
	cluster.Spec.Paused = nil
	if err := c.kubernetesClient.Patch(ctx, cluster, client.MergeFrom(before)); err != nil {
		return strategies.ClusterInfo{}, fmt.Errorf("activate Cluster %s: %w; inspect its paused state before retrying", p.Name, err)
	}
	// Desired counts are known before controllers report their observed status.
	// Leave observed replica counts at zero while provisioning starts.
	return strategies.ClusterInfo{
		Name:                 p.Name,
		Description:          p.Description,
		Phase:                "Creating",
		CreatedAt:            cluster.CreationTimestamp.String(),
		Version:              p.Version,
		ControlPlaneEndpoint: fmt.Sprintf("https://%s:%d", p.ControlPlaneIP, d.EndpointPort),
		ControlPlanesStatus:  strategies.NodeTypeStatus{DesiredReplicas: int(*p.ControlPlaneReplicas)},
		WorkersStatus:        strategies.NodeTypeStatus{DesiredReplicas: int(*worker.Replicas)},
		Tags:                 optionalClusterTags(labels),
	}, nil
}

// Replace only our two explicit tokens, leaving shell variables and command
// substitutions intact. ConfigMap values are never executed by the proxy.
func renderBootstrapCommands(config map[string]any, version, endpoint string) error {
	commands, found, err := unstructured.NestedStringSlice(config, commandsKey)
	if err != nil {
		return fmt.Errorf("invalid %s: %w", commandsKey, err)
	}
	if !found {
		return nil
	}
	replace := strings.NewReplacer("{{KUBERNETES_VERSION}}", version, "{{CONTROL_PLANE_IP}}", endpoint)
	for i, command := range commands {
		commands[i] = replace.Replace(command)
	}
	return unstructured.SetNestedStringSlice(config, commands, commandsKey)
}

func scalingConflict(reason string) error {
	return fmt.Errorf("%w: %s", strategies.ErrClusterScalingConflict, reason)
}

func (c ClusterClient) scalingCluster(ctx context.Context, name string, replicas int32) (*capiv1.Cluster, error) {
	if replicas < 1 {
		return nil, strategies.ErrInvalidReplicas
	}
	cluster, err := c.getClusterResource(ctx, name)
	if err != nil {
		return nil, err
	}
	if err := validateScalingObject(cluster, cluster); err != nil {
		return nil, err
	}
	if ptr.Deref(cluster.Spec.Paused, false) {
		return nil, scalingConflict("Cluster is paused")
	}
	if cluster.Spec.Topology.IsDefined() {
		return nil, scalingConflict("topology-managed clusters are not supported")
	}
	return cluster, nil
}

func validateScalingObject(obj client.Object, cluster *capiv1.Cluster) error {
	if !obj.GetDeletionTimestamp().IsZero() {
		return scalingConflict("resource is being deleted")
	}
	if _, paused := obj.GetAnnotations()[capiv1.PausedAnnotation]; paused {
		return scalingConflict("resource is paused")
	}
	if name, found := obj.GetLabels()[capiv1.ClusterNameLabel]; found && name != cluster.Name {
		return scalingConflict("resource belongs to another Cluster")
	}
	for _, owner := range obj.GetOwnerReferences() {
		if owner.Kind == scalingClusterKind {
			gv, err := schema.ParseGroupVersion(owner.APIVersion)
			if err != nil || gv.Group != capiv1.GroupVersion.Group || owner.Name != cluster.Name || owner.UID != cluster.UID {
				return scalingConflict("resource has a different Cluster owner")
			}
		}
	}
	return nil
}

// SetPaused changes only the Cluster spec pause field.
func (c ClusterClient) SetPaused(ctx context.Context, name string, paused bool) error {
	cluster, err := c.getClusterResource(ctx, name)
	if err != nil {
		return err
	}
	if (paused && ptr.Deref(cluster.Spec.Paused, false)) || (!paused && cluster.Spec.Paused == nil) {
		return nil
	}
	before := cluster.DeepCopy()
	cluster.Spec.Paused = nil
	if paused {
		cluster.Spec.Paused = ptr.To(true)
	}
	if err := c.kubernetesClient.Patch(ctx, cluster, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
		return pausePatchError(err, strategies.ErrClusterNotFound)
	}
	return nil
}

func (c ClusterClient) SetWorkerReplicas(ctx context.Context, name string, replicas int32) error {
	cluster, err := c.scalingCluster(ctx, name, replicas)
	if err != nil {
		return err
	}
	deployments := &capiv1.MachineDeploymentList{}
	if err := c.kubernetesClient.List(ctx, deployments, client.InNamespace(c.namespace),
		client.MatchingLabels{capiv1.ClusterNameLabel: cluster.Name}); err != nil {
		return fmt.Errorf("list worker deployments: %w", err)
	}
	if len(deployments.Items) != 1 {
		return scalingConflict("scaling requires exactly one worker MachineDeployment")
	}
	deployment := &deployments.Items[0]
	if deployment.Spec.ClusterName != cluster.Name {
		return scalingConflict("worker deployment belongs to another Cluster")
	}
	if err := validateScalingObject(deployment, cluster); err != nil {
		return err
	}
	if ptr.Deref(deployment.Spec.Paused, false) {
		return scalingConflict("worker deployment is paused")
	}
	for _, key := range []string{capiv1.AutoscalerMinSizeAnnotation, capiv1.AutoscalerMaxSizeAnnotation} {
		if _, found := deployment.Annotations[key]; found {
			return scalingConflict("worker deployment is managed by an autoscaler")
		}
	}
	if deployment.Spec.Replicas != nil && *deployment.Spec.Replicas == replicas {
		return nil
	}
	before := deployment.DeepCopy()
	deployment.Spec.Replicas = ptr.To(replicas)
	return c.patchReplicas(ctx, deployment, before)
}

func (c ClusterClient) SetControlPlaneReplicas(ctx context.Context, name string, replicas int32) error {
	cluster, err := c.scalingCluster(ctx, name, replicas)
	if err != nil {
		return err
	}
	ref := cluster.Spec.ControlPlaneRef
	if ref.Name == "" || ref.APIGroup != cpGroup || ref.Kind != controlPlaneKind {
		return scalingConflict("a KubeadmControlPlane reference is required")
	}
	controlPlane := &unstructured.Unstructured{}
	controlPlane.SetGroupVersionKind(schema.GroupVersionKind{Group: cpGroup, Version: "v1beta2", Kind: controlPlaneKind})
	if err := c.kubernetesClient.Get(ctx, client.ObjectKey{Namespace: c.namespace, Name: ref.Name}, controlPlane); err != nil {
		if apierrors.IsNotFound(err) {
			return scalingConflict("referenced KubeadmControlPlane is missing")
		}
		return fmt.Errorf("get control plane: %w", err)
	}
	if err := validateScalingObject(controlPlane, cluster); err != nil {
		return err
	}
	_, externalEtcd, err := unstructured.NestedMap(controlPlane.Object, "spec", "kubeadmConfigSpec", "clusterConfiguration", "etcd", "external")
	if err != nil {
		return fmt.Errorf("read control plane etcd configuration: %w", err)
	}
	if !externalEtcd && replicas%2 == 0 {
		return strategies.ErrInvalidControlPlaneReplicas
	}
	current, found, err := unstructured.NestedInt64(controlPlane.Object, "spec", "replicas")
	if err != nil {
		return fmt.Errorf("read control plane replicas: %w", err)
	}
	if found && current == int64(replicas) {
		return nil
	}
	before := controlPlane.DeepCopy()
	if err := unstructured.SetNestedField(controlPlane.Object, int64(replicas), "spec", "replicas"); err != nil {
		return err
	}
	return c.patchReplicas(ctx, controlPlane, before)
}

func (c ClusterClient) patchReplicas(ctx context.Context, obj, before client.Object) error {
	err := c.kubernetesClient.Patch(ctx, obj, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{}))
	switch {
	case apierrors.IsConflict(err), apierrors.IsNotFound(err):
		return scalingConflict("target changed during scaling; read its state before retrying")
	case apierrors.IsInvalid(err):
		// Only the replicas field is changed. Keep webhook details internal.
		return fmt.Errorf("%w: %w", strategies.ErrReplicasRejected, err)
	case err != nil:
		return fmt.Errorf("patch replicas: %w", err)
	default:
		return nil
	}
}

func pausePatchError(err, notFound error) error {
	switch {
	case apierrors.IsNotFound(err):
		return fmt.Errorf("%w: %w", notFound, err)
	case apierrors.IsConflict(err):
		return fmt.Errorf("%w: %w", strategies.ErrPauseUpdateConflict, err)
	default:
		return fmt.Errorf("patch pause state: %w", err)
	}
}

// SetDescription patches only the description annotation, preserving other metadata.
func (c ClusterClient) SetDescription(ctx context.Context, name, description string) error {
	cluster, err := c.getClusterResource(ctx, name)
	if err != nil {
		return err
	}
	if cluster.Annotations[clusterDescriptionAnnotation] == description {
		return nil
	}
	before := cluster.DeepCopy()
	if description == "" {
		delete(cluster.Annotations, clusterDescriptionAnnotation)
	} else {
		if cluster.Annotations == nil {
			cluster.Annotations = make(map[string]string)
		}
		cluster.Annotations[clusterDescriptionAnnotation] = description
	}
	if err := c.kubernetesClient.Patch(ctx, cluster, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
		if apierrors.IsNotFound(err) {
			return strategies.ErrClusterNotFound
		}
		if apierrors.IsConflict(err) {
			return strategies.ErrClusterDescriptionUpdateConflict
		}
		return fmt.Errorf("patch cluster description: %w", err)
	}
	return nil
}
