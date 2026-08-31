package kubernetes_clients

import (
	"context"

	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type MachineClient struct {
	kubernetesClient client.Client
	namespace        string
}

func NewMachineClient(kubernetesClient client.Client, namespace string) MachineClient {
	return MachineClient{kubernetesClient, namespace}
}

func (m MachineClient) GetMachine(name string) (*capiv1.Machine, error) {
	machine := &capiv1.Machine{}
	if err := m.kubernetesClient.Get(
		context.TODO(),
		client.ObjectKey{Namespace: m.namespace, Name: name},
		machine,
	); err != nil {
		return &capiv1.Machine{}, err
	}

	return machine, nil
}
