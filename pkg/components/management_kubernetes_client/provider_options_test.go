package management_kubernetes_client

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/vatesfr/vmk-engine/pkg/strategies"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestRC6ProviderConfiguration(t *testing.T) {
	data := validConfigMapData(map[string]string{
		xoTagsKey: "- production\n- platform-team",
		ciliumKey: "kubeProxyReplacement: true\nk8sServiceHost: 192.0.2.10\nk8sServicePort: 6443",
		controlPlaneMachineSpecKey: `templateID: image
bootstrap:
  format: cloudinit
tags:
  - control-plane
networkConfig:
  networks:
    - networkID: network
  addressesFromPools:
    - apiGroup: ipam.cluster.x-k8s.io
      kind: InClusterIPPool
      name: control-plane-pool
`,
	})
	defaults, err := parseClusterDefaults(data)
	require.NoError(t, err)
	p, _, scheme := newClusterCreationTestSetup(t)
	p.Defaults = defaults
	kube := fake.NewClientBuilder().WithScheme(scheme).Build()
	_, err = NewClusterClient(kube, clientAsCache{kube}, testCreationNamespace).Create(t.Context(), p)
	require.NoError(t, err)

	xo := resource(xoGroup, xoClusterKind, p.Name, testCreationNamespace, nil)
	require.NoError(t, kube.Get(t.Context(), client.ObjectKeyFromObject(xo), xo))
	tags, _, err := unstructured.NestedStringSlice(xo.Object, specKey, "tags")
	require.NoError(t, err)
	require.Equal(t, []string{"production", "platform-team"}, tags)
	config, _, err := unstructured.NestedMap(xo.Object, specKey, "addons", ciliumKey)
	require.NoError(t, err)
	require.Equal(t, map[string]any{
		"kubeProxyReplacement": true, "k8sServiceHost": "192.0.2.10", "k8sServicePort": int64(6443),
	}, config)

	machine := resource(xoGroup, xoMachineTemplateKind, testControlPlane, testCreationNamespace, nil)
	require.NoError(t, kube.Get(t.Context(), client.ObjectKeyFromObject(machine), machine))
	stored, _, err := unstructured.NestedMap(machine.Object, specKey, templateKey, specKey)
	require.NoError(t, err)
	delete(stored, namePrefixKey)
	require.Equal(t, defaults.ControlPlaneMachineSpec, stored)

	cluster := &capiv1.Cluster{}
	require.NoError(t, kube.Get(t.Context(), client.ObjectKey{Namespace: testCreationNamespace, Name: p.Name}, cluster))
	require.Equal(t, "test", cluster.Labels[clusterTagLabelPrefix+"env"])
	require.NotContains(t, cluster.Labels, clusterTagLabelPrefix+"production")
}

func TestInvalidProviderOptions(t *testing.T) {
	for name, overrides := range map[string]map[string]string{
		"tags must be a list":           {xoTagsKey: "team: platform"},
		"tags must be strings":          {xoTagsKey: "- {}"},
		"empty tag":                     {xoTagsKey: "- ''"},
		"duplicate tags":                {xoTagsKey: "[production, production]"},
		"long tag":                      {xoTagsKey: "- " + strings.Repeat("a", 256)},
		"too many tags":                 {xoTagsKey: "[" + strings.Repeat("tag,", 64) + "last]"},
		"invalid Cilium YAML":           {ciliumKey: "[broken"},
		"Cilium must be an object":      {ciliumKey: "[]"},
		"unknown Cilium option":         {ciliumKey: "kubeProxyReplacment: true"},
		"replacement requires Cilium":   {ciliumKey: "kubeProxyReplacement: true", cniKey: "none"},
		"host requires port":            {ciliumKey: "kubeProxyReplacement: true\nk8sServiceHost: api.example.org"},
		"port requires host":            {ciliumKey: "kubeProxyReplacement: true\nk8sServicePort: 6443"},
		"endpoint requires replacement": {ciliumKey: "k8sServiceHost: api.example.org\nk8sServicePort: 6443"},
		"negative port":                 {ciliumKey: "kubeProxyReplacement: true\nk8sServiceHost: api.example.org\nk8sServicePort: -1"},
		"port out of range":             {ciliumKey: "kubeProxyReplacement: true\nk8sServiceHost: api.example.org\nk8sServicePort: 65536"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := parseClusterDefaults(validConfigMapData(overrides))
			require.Error(t, err)
		})
	}
}

func TestOptionalProviderOptions(t *testing.T) {
	for _, raw := range []string{"", "{}", "kubeProxyReplacement: false", "kubeProxyReplacement: true"} {
		t.Run(raw, func(t *testing.T) {
			_, err := parseClusterDefaults(validConfigMapData(map[string]string{ciliumKey: raw, xoTagsKey: "[]"}))
			require.NoError(t, err)
		})
	}
}

func TestInvalidProviderOptionsDoNotCreateResources(t *testing.T) {
	p, _, scheme := newClusterCreationTestSetup(t)
	p.Defaults.CNI = "none"
	p.Defaults.Cilium = &strategies.CiliumConfig{KubeProxyReplacement: true}
	kube := fake.NewClientBuilder().WithScheme(scheme).Build()
	_, err := NewClusterClient(kube, clientAsCache{kube}, testCreationNamespace).Create(t.Context(), p)
	require.ErrorContains(t, err, "requires cni to be cilium")
	clusters := &capiv1.ClusterList{}
	require.NoError(t, kube.List(t.Context(), clusters))
	require.Empty(t, clusters.Items)
}
