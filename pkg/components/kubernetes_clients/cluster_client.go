package kubernetes_clients

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"capv-proxy/pkg/strategies"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/utils/ptr"
)

const clusterTagLabelPrefix = "tags.vmk.vates.tech/"

type ClusterClient struct {
	kubernetesClient client.Client
	namespace        string
}

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

	clusterResource := &capiv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      params.Name,
			Namespace: c.namespace,
			Labels:    labels,
		},
		Spec: capiv1.ClusterSpec{
			Topology: capiv1.Topology{
				ClassRef: capiv1.ClusterClassRef{
					Name: params.ClusterClassName,
				},
				Version: params.KubernetesVersion,
				ControlPlane: capiv1.ControlPlaneTopology{
					Replicas: params.ControlPlaneReplicas,
				},
				Workers: capiv1.WorkersTopology{
					MachineDeployments: toMachineDeployments(params.MachineDeployments),
				},
			},
		},
	}

	variables, err := toClusterVariables(params.Variables)
	if err != nil {
		return strategies.ClusterInfo{}, err
	}
	clusterResource.Spec.Topology.Variables = variables

	if err := c.kubernetesClient.Create(ctx, clusterResource); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return strategies.ClusterInfo{}, fmt.Errorf("%w: %w", strategies.ErrClusterAlreadyExists, err)
		}
		return strategies.ClusterInfo{}, fmt.Errorf("create Cluster %s/%s: %w", c.namespace, params.Name, err)
	}

	return strategies.ClusterInfo{
		Name:  clusterResource.GetName(),
		Phase: "Creating",
		Tags:  optionalClusterTags(clusterResource.Labels),
	}, nil
}

func toMachineDeployments(machineDeployments []strategies.MachineDeployment) []capiv1.MachineDeploymentTopology {
	result := make([]capiv1.MachineDeploymentTopology, 0, len(machineDeployments))
	for _, machineDeployment := range machineDeployments {
		result = append(result, capiv1.MachineDeploymentTopology{
			Name:          machineDeployment.Name,
			Class:         machineDeployment.Class,
			Replicas:      machineDeployment.Replicas,
			FailureDomain: machineDeployment.FailureDomain,
		})
	}
	return result
}

func toClusterVariables(variables map[string]any) ([]capiv1.ClusterVariable, error) {
	names := make([]string, 0, len(variables))
	for name := range variables {
		names = append(names, name)
	}
	sort.Strings(names)

	result := make([]capiv1.ClusterVariable, 0, len(names))
	for _, name := range names {
		value, err := json.Marshal(variables[name])
		if err != nil {
			return nil, fmt.Errorf("encode cluster variable %q: %w", name, err)
		}
		result = append(result, capiv1.ClusterVariable{
			Name:  name,
			Value: apiextensionsv1.JSON{Raw: value},
		})
	}
	return result, nil
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

	return nil
}

func toInfo(value *capiv1.Cluster) strategies.ClusterInfo {
	controlPlaneEndpoint := ""
	if value.Spec.ControlPlaneEndpoint.IsValid() {
		controlPlaneEndpoint = "https://" + value.Spec.ControlPlaneEndpoint.String()
	}

	info := strategies.ClusterInfo{
		Name:                 value.GetName(),
		Phase:                value.Status.Phase,
		ControlPlaneEndpoint: controlPlaneEndpoint,
		Version:              value.Spec.Topology.Version,
		Tags:                 optionalClusterTags(value.Labels),
	}
	info.CreatedAt = value.CreationTimestamp.String()
	if status := value.Status.ControlPlane; status != nil {
		info.ControlPlanesStatus = strategies.NodeTypeStatus{
			AvailableReplicas: int(ptr.Deref(status.AvailableReplicas, 0)),
			DesiredReplicas:   int(ptr.Deref(status.DesiredReplicas, 0)),
			ReadyReplicas:     int(ptr.Deref(status.ReadyReplicas, 0)),
			Replicas:          int(ptr.Deref(status.Replicas, 0)),
		}
	}
	if status := value.Status.Workers; status != nil {
		info.WorkersStatus = strategies.NodeTypeStatus{
			AvailableReplicas: int(ptr.Deref(status.AvailableReplicas, 0)),
			DesiredReplicas:   int(ptr.Deref(status.DesiredReplicas, 0)),
			ReadyReplicas:     int(ptr.Deref(status.ReadyReplicas, 0)),
			Replicas:          int(ptr.Deref(status.Replicas, 0)),
		}
	}
	return info
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

func (c ClusterClient) GetTags(ctx context.Context, clusterName string) (map[string]string, error) {
	cluster, err := c.getClusterResource(ctx, clusterName)
	if err != nil {
		return nil, err
	}
	return clusterTagsFromLabels(cluster.Labels), nil
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
