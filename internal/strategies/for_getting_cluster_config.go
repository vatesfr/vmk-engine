package strategies

//go:generate go run -mod=mod go.uber.org/mock/mockgen -source=for_getting_cluster_config.go -destination=mocks/for_getting_cluster_config.go -package=mocks
type ForGettingClusterConfig interface {
	Get(clusterName string) (string, error)
}
