package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"capv-proxy/pkg/components/cluster_defaults"
	"capv-proxy/pkg/components/cluster_manager"
	"capv-proxy/pkg/components/kubernetes_clients"
	"capv-proxy/pkg/components/tag_db"
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
	k8sClient, err := newK8sClient(opts.kubeClient, opts.kubeconfig)
	if err != nil {
		return err
	}

	k8sClusterClient := kubernetes_clients.NewClusterClient(k8sClient, opts.namespace)
	k8sSecretClient := kubernetes_clients.NewSecretClient(k8sClient, opts.namespace)
	k8sMachineClient := kubernetes_clients.NewMachineClient(k8sClient, opts.namespace)
	clusterDefaults := cluster_defaults.NewClusterDefaults()
	clusterManager := cluster_manager.NewClusterManager(k8sClusterClient, k8sSecretClient, k8sMachineClient, clusterDefaults)

	httpServer := webserver.HttpServer{
		Address:   opts.address,
		Port:      opts.port,
		SwaggerUI: opts.swaggerUI,
	}
	if err := httpServer.Run(clusterManager, tag_db.NewTagDB()); err != nil {
		return fmt.Errorf("run HTTP server: %w", err)
	}

	return nil
}

func newK8sClient(clientType, kubeconfig string) (client.Client, error) {
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
		return nil, fmt.Errorf("build Kubernetes %s config: %w", clientType, err)
	}

	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("register Kubernetes types: %w", err)
	}
	if err := clusterv1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("register Cluster API types: %w", err)
	}

	kubernetesClient, err := client.New(config, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("create Cluster API client: %w", err)
	}

	return kubernetesClient, nil
}
