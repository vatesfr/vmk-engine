package kubernetes_clients

import (
	"context"
	"fmt"
	"net"
	"sort"

	"capv-proxy/pkg/strategies"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type MachineClient struct {
	kubernetesClient client.Client
	namespace        string
}

var _ strategies.ForGettingClusterMachines = MachineClient{}

func NewMachineClient(kubernetesClient client.Client, namespace string) MachineClient {
	return MachineClient{kubernetesClient, namespace}
}

func (m MachineClient) GetMachinesByCluster(clusterName string) ([]strategies.ClusterNodeInfo, error) {
	if clusterName == "" {
		return nil, fmt.Errorf("cluster name is required")
	}
	if m.namespace == "" {
		return nil, fmt.Errorf("cluster namespace is required")
	}

	machines := &capiv1.MachineList{}
	if err := m.kubernetesClient.List(
		context.Background(),
		machines,
		client.InNamespace(m.namespace),
		client.MatchingLabels{capiv1.ClusterNameLabel: clusterName},
	); err != nil {
		return nil, fmt.Errorf("list Machines for cluster %s/%s: %w", m.namespace, clusterName, err)
	}

	result := make([]strategies.ClusterNodeInfo, 0, len(machines.Items))
	for i := range machines.Items {
		result = append(result, toClusterNodeInfo(&machines.Items[i]))
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

func toClusterNodeInfo(machine *capiv1.Machine) strategies.ClusterNodeInfo {
	name := machine.Name
	if machine.Status.NodeRef.IsDefined() {
		name = machine.Status.NodeRef.Name
	}

	role := "worker"
	if _, isControlPlane := machine.Labels[capiv1.MachineControlPlaneLabel]; isControlPlane {
		role = "control-plane"
	}

	status := machine.Status.Phase
	if ready := apimeta.FindStatusCondition(machine.Status.Conditions, capiv1.MachineReadyCondition); ready != nil {
		switch ready.Status {
		case metav1.ConditionTrue:
			status = "Ready"
		case metav1.ConditionFalse:
			status = "NotReady"
		default:
			status = "Unknown"
		}
	}
	if status == "" {
		status = "Unknown"
	}

	return strategies.ClusterNodeInfo{
		Name:     name,
		Role:     role,
		Status:   status,
		Endpoint: machineEndpoint(machine.Status.Addresses),
	}
}

func machineEndpoint(addresses capiv1.MachineAddresses) string {
	for _, addressType := range []capiv1.MachineAddressType{
		capiv1.MachineInternalIP,
		capiv1.MachineExternalIP,
		capiv1.MachineHostName,
	} {
		for _, address := range addresses {
			if address.Type == addressType && address.Address != "" {
				return "https://" + net.JoinHostPort(address.Address, "10250")
			}
		}
	}
	return ""
}
