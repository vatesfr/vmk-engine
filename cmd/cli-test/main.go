package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func main() {
	kubeconfig := flag.String(
		"kubeconfig",
		filepath.Join(homedir.HomeDir(), ".kube", "config"),
		"absolute path to the kubeconfig file",
	)
	namespace := flag.String(
		"namespace",
		"",
		"namespace to query (empty means all namespaces)",
	)
	flag.Parse()

	config, err := clientcmd.BuildConfigFromFlags("", *kubeconfig)
	if err != nil {
		exitOnError("build Kubernetes config", err)
	}

	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		exitOnError("register Kubernetes types", err)
	}
	if err := clusterv1.AddToScheme(scheme); err != nil {
		exitOnError("register Cluster API types", err)
	}

	kubernetesClient, err := client.New(config, client.Options{Scheme: scheme})
	if err != nil {
		exitOnError("create Cluster API client", err)
	}

	clusters := &clusterv1.ClusterList{}
	listOptions := []client.ListOption{}
	if *namespace != "" {
		listOptions = append(listOptions, client.InNamespace(*namespace))
	}

	if err := kubernetesClient.List(context.Background(), clusters, listOptions...); err != nil {
		exitOnError("list clusters", err)
	}

	if len(clusters.Items) == 0 {
		fmt.Println("No Cluster API clusters found.")
		return
	}

	for _, cluster := range clusters.Items {
		fmt.Printf("%s/%s\n", cluster.Namespace, cluster.Name)
	}
}

func exitOnError(action string, err error) {
	fmt.Fprintf(os.Stderr, "%s: %v\n", action, err)
	os.Exit(1)
}
