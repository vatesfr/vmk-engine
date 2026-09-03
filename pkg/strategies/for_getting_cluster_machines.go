package strategies

//go:generate go run -mod=mod go.uber.org/mock/mockgen -source=for_getting_cluster_machines.go -destination=mocks/for_getting_cluster_machines.go -package=mocks
type ForGettingClusterMachines interface {
	GetMachinesByCluster(clusterName string) ([]ClusterNodeInfo, error)
}
