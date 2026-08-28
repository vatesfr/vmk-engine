package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"capv-proxy/internal/adapters"
	"capv-proxy/internal/kubernetes_clients"
	"capv-proxy/internal/webserver"

	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func main() {
	httpServer := webserver.HttpServer{}

	kubeconfig := flag.String("kubeconfig", filepath.Join(homedir.HomeDir(), ".kube", "config"), "absolute path to the kubeconfig file")
	namespace := flag.String("namespace", "default", "Kubernetes namespace containing the Cluster API resources")
	flag.Parse()

	k8sClient := newK8sClient(*kubeconfig)

	k8sClusterClient := kubernetes_clients.NewClusterClient(k8sClient, *namespace)
	k8sMachineClient := kubernetes_clients.NewMachineClient(k8sClient, *namespace)
	k8sSecretClient := kubernetes_clients.NewSecretClient(k8sClient, *namespace)

	k8sHTTPAdapter := adapters.NewK8sHTTPAdapter(k8sClusterClient, k8sMachineClient, k8sSecretClient)

	if err := httpServer.Run(k8sHTTPAdapter); err != nil {
		log.Fatalf("failed to run server: %v", err)
	}
}

func newK8sClient(kubeconfig string) client.Client {
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
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

	return kubernetesClient
}

func exitOnError(action string, err error) {
	fmt.Fprintf(os.Stderr, "%s: %v\n", action, err)
	os.Exit(1)
}
