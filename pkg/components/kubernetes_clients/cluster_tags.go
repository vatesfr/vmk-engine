package kubernetes_clients

import (
	"context"
	"fmt"
	"strings"

	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"capv-proxy/pkg/strategies"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"
)

const clusterTagLabelPrefix = "tags.vmk.vates.tech/"

var _ strategies.ForManagingClusterTagResources = ClusterClient{}

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
	tags := make(map[string]string)
	for name, value := range cluster.Labels {
		if strings.HasPrefix(name, clusterTagLabelPrefix) {
			tags[strings.TrimPrefix(name, clusterTagLabelPrefix)] = value
		}
	}
	return tags, nil
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
