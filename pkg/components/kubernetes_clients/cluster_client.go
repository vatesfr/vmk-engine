package kubernetes_clients

import (
	"context"
	"fmt"
	"net/netip"
	"strings"

	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"capv-proxy/pkg/strategies"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/utils/ptr"
)

type ClusterClient struct {
	kubernetesClient client.Client
	namespace        string
}

const (
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

func NewClusterClient(kubernetesClient client.Client, namespace string) ClusterClient {
	return ClusterClient{kubernetesClient, namespace}
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
	if err := c.kubernetesClient.List(
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
	if err := c.kubernetesClient.Get(
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
	if err := c.kubernetesClient.List(ctx, clusters, client.InNamespace(c.namespace)); err != nil {
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
	cluster, err := c.getClusterResource(ctx, clusterName)
	if err != nil {
		return nil, err
	}
	return clusterTagsFromLabels(cluster.Labels), nil
}

func (c ClusterClient) GetTag(ctx context.Context, clusterName, tag string) (string, error) {
	if err := validateClusterTag(tag, ""); err != nil {
		return "", err
	}
	cluster, err := c.getClusterResource(ctx, clusterName)
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
		IsAvailable:          apimeta.IsStatusConditionTrue(value.Status.Conditions, capiv1.ClusterAvailableCondition),
		Name:                 value.GetName(),
		Description:          value.Annotations[clusterDescriptionAnnotation],
		Phase:                value.Status.Phase,
		ControlPlaneEndpoint: controlPlaneEndpoint,
		Version:              value.Spec.Topology.Version,
		Tags:                 optionalClusterTags(value.Labels),
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
	return strategies.ClusterInfo{Name: p.Name, Description: p.Description, Phase: "Creating", Tags: optionalClusterTags(labels)}, nil
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
