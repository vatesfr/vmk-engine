package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"capv-proxy/pkg/components/cluster_manager"
	"capv-proxy/pkg/components/management_kubernetes_client"
	versionmanager "capv-proxy/pkg/components/version_manager"
	"capv-proxy/pkg/components/webserver"

	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

const kubeClientKubeconfig = "kubeconfig"

func main() {
	if err := newRootCommand().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type options struct {
	kubeClient string
	kubeconfig string
	namespace  string
	address    string
	port       uint16
	swaggerUI  string
}

func newRootCommand() *cobra.Command {
	opts := options{
		kubeClient: kubeClientKubeconfig,
		kubeconfig: filepath.Join(homedir.HomeDir(), ".kube", "config"),
		namespace:  "default",
		address:    "0.0.0.0",
		port:       8080,
		swaggerUI:  webserver.SwaggerUIStoplight,
	}

	cmd := &cobra.Command{
		Use:           "http_server",
		Short:         "Expose Cluster API resources over HTTP",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		PreRunE: func(_ *cobra.Command, _ []string) error {
			return opts.validate()
		},
		RunE: func(_ *cobra.Command, _ []string) error {
			return run(opts)
		},
	}

	flags := cmd.Flags()
	flags.StringVar(&opts.kubeClient, "kube-client", opts.kubeClient, "Kubernetes client configuration: kubeconfig or in-cluster")
	flags.StringVar(&opts.kubeconfig, "kubeconfig", opts.kubeconfig, "absolute path to the kubeconfig file (with --kube-client=kubeconfig)")
	flags.StringVarP(&opts.namespace, "namespace", "n", opts.namespace, "Kubernetes namespace containing the Cluster API resources")
	flags.StringVar(&opts.address, "address", opts.address, "address on which the HTTP server listens")
	flags.Uint16VarP(&opts.port, "port", "p", opts.port, "port on which the HTTP server listens")
	flags.StringVar(&opts.swaggerUI, "swagger-ui", opts.swaggerUI, "OpenAPI UI: swagger, stoplight or disabled")

	return cmd
}

func (o options) validate() error {
	if o.kubeClient != kubeClientKubeconfig && o.kubeClient != "in-cluster" {
		return fmt.Errorf("invalid --kube-client %q: must be kubeconfig or in-cluster", o.kubeClient)
	}
	if o.kubeClient == kubeClientKubeconfig && o.kubeconfig == "" {
		return fmt.Errorf("--kubeconfig must not be empty when --kube-client=kubeconfig")
	}
	if o.namespace == "" {
		return fmt.Errorf("--namespace must not be empty")
	}
	if o.address == "" {
		return fmt.Errorf("--address must not be empty")
	}
	if o.port == 0 {
		return fmt.Errorf("--port must be greater than zero")
	}
	if !webserver.IsSwaggerUI(o.swaggerUI) {
		return fmt.Errorf("invalid --swagger-ui %q: must be swagger, stoplight or disabled", o.swaggerUI)
	}

	return nil
}

func run(opts options) error {
	ctx := context.Background()

	restConfig, scheme, err := buildRestConfig(opts.kubeClient, opts.kubeconfig)
	if err != nil {
		return err
	}

	k8sClient, err := newK8sClient(restConfig, scheme)
	if err != nil {
		return err
	}

	informerCache, err := newInformerCache(restConfig, scheme, opts.namespace)
	if err != nil {
		return err
	}
	if err := management_kubernetes_client.RegisterIndexes(ctx, informerCache); err != nil {
		return fmt.Errorf("register UUID indexers: %w", err)
	}
	go func() {
		if err := informerCache.Start(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "informer cache stopped: %v\n", err)
		}
	}()
	if !informerCache.WaitForCacheSync(ctx) {
		return fmt.Errorf("informer cache failed to sync")
	}

	k8sClusterClient := management_kubernetes_client.NewClusterClient(k8sClient, informerCache, opts.namespace)
	k8sSecretClient := management_kubernetes_client.NewSecretClient(k8sClient, opts.namespace)
	k8sMachineClient := management_kubernetes_client.NewMachineClient(k8sClient, informerCache, opts.namespace)
	k8sConfigMapClient := management_kubernetes_client.NewConfigMapClient(k8sClient, opts.namespace)
	versionManager := versionmanager.NewVersionManager()
	clusterManager := cluster_manager.NewClusterManager(
		k8sClusterClient, k8sSecretClient, k8sMachineClient, k8sConfigMapClient, versionManager,
	)

	httpServer := webserver.HttpServer{
		Address:   opts.address,
		Port:      opts.port,
		SwaggerUI: opts.swaggerUI,
	}
	if err := httpServer.Run(clusterManager, versionManager); err != nil {
		return fmt.Errorf("run HTTP server: %w", err)
	}

	return nil
}

func buildRestConfig(clientType, kubeconfig string) (*rest.Config, *runtime.Scheme, error) {
	var (
		config *rest.Config
		err    error
	)
	if clientType == "in-cluster" {
		config, err = rest.InClusterConfig()
	} else {
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("build Kubernetes %s config: %w", clientType, err)
	}

	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		return nil, nil, fmt.Errorf("register Kubernetes types: %w", err)
	}
	if err := clusterv1.AddToScheme(scheme); err != nil {
		return nil, nil, fmt.Errorf("register Cluster API types: %w", err)
	}

	return config, scheme, nil
}

func newK8sClient(config *rest.Config, scheme *runtime.Scheme) (client.Client, error) {
	k8sClient, err := client.New(config, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("create Cluster API client: %w", err)
	}
	return k8sClient, nil
}

func newInformerCache(config *rest.Config, scheme *runtime.Scheme, namespace string) (cache.Cache, error) {
	informerCache, err := cache.New(config, cache.Options{
		Scheme: scheme,
		DefaultNamespaces: map[string]cache.Config{
			namespace: {},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("create informer cache: %w", err)
	}
	return informerCache, nil
}
