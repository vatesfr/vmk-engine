package strategies

//go:generate go run -mod=mod go.uber.org/mock/mockgen -source=for_getting_cluster_config.go -destination=mock/for_getting_cluster_config.go -package=mock

type ForGettingClusterConfig interface {
	Get(clusterName string) (string, error)
}
