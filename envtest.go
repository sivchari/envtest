package envtest

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/onsi/ginkgo/v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/component-base/logs"
	logsv1 "k8s.io/component-base/logs/api/v1"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/log"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

func init() {
	logOptions := logs.NewOptions()
	logOptions.Verbosity = logsv1.VerbosityLevel(3)
	if err := logsv1.ValidateAndApply(logOptions, nil); err != nil {
		klog.ErrorS(err, "unable to validate and apply log options")
		os.Exit(1)
	}

	logger := klog.Background()
	ctrl.SetLogger(logger)
	log.SetLogger(logger)
	klog.SetOutput(ginkgo.GinkgoWriter)

	utilruntime.Must(scheme.AddToScheme(scheme.Scheme))
}

type Input struct {
	M                 *testing.M
	CRDDirectoryPaths []string
	SetupReconcilers  func(ctx context.Context, mgr ctrl.Manager)
	SetupIndexers     func(ctx context.Context, mgr ctrl.Manager)
	SetupEnv          func(e *Environment)
}

func Run(ctx context.Context, input Input) int {
	env := newEnvironment(input.CRDDirectoryPaths)

	ctx, cancel := context.WithCancel(ctx)
	env.cancelFunc = cancel

	klog.V(1).Info("starting envtest")
	if input.SetupEnv != nil {
		input.SetupEnv(env)
	}

	if input.SetupIndexers != nil {
		input.SetupIndexers(ctx, env.manager)
	}

	if input.SetupReconcilers != nil {
		input.SetupReconcilers(ctx, env.manager)
	}

	env.start(ctx)

	defer func() {
		if err := env.stop(); err != nil {
			klog.Fatalf("unable to stop envtest: %v", err)
		}
	}()

	return input.M.Run()
}

type Environment struct {
	client.Client
	*envtest.Environment
	manager    ctrl.Manager
	cancelFunc context.CancelFunc
}

func newEnvironment(crdDirectoryPaths []string) *Environment {
	env := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}

	if crdDirectoryPaths != nil {
		env.CRDDirectoryPaths = crdDirectoryPaths
	}

	if _, err := env.Start(); err != nil {
		klog.Fatalf("failed to start envtest: %v", err)
	}

	mgr, err := ctrl.NewManager(env.Config, ctrl.Options{
		Scheme: scheme.Scheme,
		Metrics: metricsserver.Options{
			BindAddress: "0",
		},
	})
	if err != nil {
		klog.Fatalf("unable to start manager: %v", err)
	}

	return &Environment{
		Client:      mgr.GetClient(),
		manager:     mgr,
		Environment: env,
	}
}

func (e *Environment) start(ctx context.Context) {
	go func() {
		if err := e.manager.Start(ctx); err != nil {
			klog.Fatalf("failed to start manager: %v", err)
		}
	}()
	<-e.manager.Elected()
}

func (e *Environment) stop() error {
	klog.V(1).Info("stopping envtest")
	e.cancelFunc()
	return e.Stop()
}

func (e *Environment) CreateNamespace(ctx context.Context, ns string) (*corev1.Namespace, error) {
	namespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: fmt.Sprintf("%s-", ns),
		},
	}
	if err := e.Create(ctx, namespace); err != nil {
		return nil, err
	}
	return namespace, nil
}
