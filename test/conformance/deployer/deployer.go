// Copyright 2026 HAProxy Technologies LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package deployer implements a lightweight Kubernetes deployer for conformance
// testing: it watches Gateway objects and provisions a dedicated HUG controller
// Deployment and Service for each one.
package deployer

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

const (
	labelGatewayNSKey   = "hug.haproxy.com/gateway-namespace"
	labelGatewayNameKey = "hug.haproxy.com/gateway-name"
	hugServiceLabelKey  = "app.kubernetes.io/name"

	defaultControllerNS    = "haproxy-unified-gateway"
	defaultControllerLabel = "haproxy-unified-gateway"
)

// Config holds the parameters for the Gateway deployer.
type Config struct {
	// DeployerNs is the namespace where controller Deployments and Services are created.
	DeployerNs string
	// ControllerImage is the HUG container image to deploy (e.g. "haproxytech/haproxy-unified-gateway:latest").
	ControllerImage string
	// HugConfCRD is the namespace/name of the HugConf custom resource (e.g. "haproxy-unified-gateway/hugconf").
	HugConfCRD string
	// GatewayClassName, when non-empty, restricts the deployer to Gateways of this class.
	GatewayClassName string
	// ServiceType is the Kubernetes ServiceType for the provisioned Services.
	// Defaults to LoadBalancer; use ClusterIP in environments without a load-balancer (e.g. CI).
	ServiceType corev1.ServiceType
	// WatchNamespaces, when non-empty, restricts the deployer to Gateways in these namespaces only.
	WatchNamespaces []string
}

// Start creates a controller-runtime manager, registers the GatewayReconciler,
// and runs until ctx is cancelled. It returns:
//   - scaledDown: receives exactly one value when ScaleDefaultControllerToZero completes.
//   - restoreDone: closed when the default controller has been scaled back to one replica.
//
// Callers must wait on scaledDown before relying on the standard controller being inactive,
// and must wait on restoreDone after cancelling ctx to ensure the restore completes.
func Start(ctx context.Context, restCfg *rest.Config, cfg Config) (scaledDown <-chan error, restoreDone <-chan struct{}, err error) {
	scheme := runtime.NewScheme()
	utilruntime.Must(appsv1.AddToScheme(scheme))
	utilruntime.Must(corev1.AddToScheme(scheme))
	utilruntime.Must(gatewayv1.Install(scheme))

	mgrOpts := ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress: "0",
	}
	if len(cfg.WatchNamespaces) > 0 {
		ns := make(map[string]cache.Config, len(cfg.WatchNamespaces))
		for _, n := range cfg.WatchNamespaces {
			ns[n] = cache.Config{}
		}
		mgrOpts.Cache = cache.Options{DefaultNamespaces: ns}
	}
	mgr, err := ctrl.NewManager(restCfg, mgrOpts)
	if err != nil {
		return nil, nil, fmt.Errorf("creating manager: %w", err)
	}

	r := &GatewayReconciler{
		Client: mgr.GetClient(),
		Scheme: scheme,
		Config: cfg,
	}
	if err := r.SetupWithManager(mgr); err != nil {
		return nil, nil, fmt.Errorf("setting up reconciler: %w", err)
	}

	_, _ = fmt.Fprintln(os.Stderr, "deployer: manager created, starting")
	go func() {
		if err := mgr.Start(ctx); err != nil && ctx.Err() == nil {
			_, _ = fmt.Fprintf(os.Stderr, "deployer: manager exited: %v\n", err)
		}
	}()

	_, _ = fmt.Fprintln(os.Stderr, "deployer: waiting for cache sync")
	if !mgr.GetCache().WaitForCacheSync(ctx) {
		_, _ = fmt.Fprintln(os.Stderr, "deployer: cache sync failed")
		return nil, nil, errors.New("deployer cache sync failed")
	}
	_, _ = fmt.Fprintln(os.Stderr, "deployer: cache synced")

	deployerDone := make(chan error, 1)
	restored := make(chan struct{})

	go func() {
		defer close(restored)
		_, _ = fmt.Fprintln(os.Stderr, "deployer: scaling down default controller")
		restore, err := ScaleDefaultControllerToZero(ctx, mgr.GetAPIReader(), mgr.GetClient())
		deployerDone <- err
		if err != nil {
			return
		}
		_, _ = fmt.Fprintln(os.Stderr, "deployer: scale-down done, watching for gateways")
		<-ctx.Done()
		// Use a fresh client: the manager's client may be shutting down after ctx is cancelled.
		restoreClient, err := client.New(restCfg, client.Options{Scheme: scheme})
		if err != nil {
			ctrl.Log.Error(err, "creating restore client")
			return
		}
		restore(context.Background(), restoreClient)
	}()

	return deployerDone, restored, nil
}

// GatewayReconciler watches Gateway objects and reconciles a HUG controller
// Deployment and Service in Config.OperatorNS for each one.
type GatewayReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Config Config
}

// SetupWithManager registers the reconciler with the manager.
func (r *GatewayReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&gatewayv1.Gateway{}).
		Complete(r)
}

// Reconcile is called for every Gateway create/update/delete event.
func (r *GatewayReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_, _ = fmt.Fprintf(os.Stderr, "deployer: reconcile triggered gateway=%s\n", req.NamespacedName)
	gw := &gatewayv1.Gateway{}
	if err := r.Get(ctx, req.NamespacedName, gw); err != nil {
		if k8serrors.IsNotFound(err) {
			_, _ = fmt.Fprintf(os.Stderr, "deployer: gateway deleted, cleaning up %s\n", req.NamespacedName)
			return ctrl.Result{}, r.deleteResources(ctx, req.Namespace, req.Name)
		}
		return ctrl.Result{}, err
	}

	if r.Config.GatewayClassName != "" && string(gw.Spec.GatewayClassName) != r.Config.GatewayClassName {
		_, _ = fmt.Fprintf(os.Stderr, "deployer: skipping %s class=%s expected=%s\n", req.NamespacedName, gw.Spec.GatewayClassName, r.Config.GatewayClassName)
		return ctrl.Result{}, nil
	}

	_, _ = fmt.Fprintf(os.Stderr, "deployer: reconciling resources for %s\n", req.NamespacedName)
	return ctrl.Result{}, r.reconcileResources(ctx, gw)
}

// reconcileResources ensures a Deployment and Service exist for gw.
func (r *GatewayReconciler) reconcileResources(ctx context.Context, gw *gatewayv1.Gateway) error {
	// resourceName is the Kubernetes name for the Deployment and Service created for this Gateway.
	// It is derived from the Gateway's namespace and name to ensure uniqueness and satisfy DNS label requirements.
	resourceName := resourceName(gw.Namespace, gw.Name)
	// hugSvcFlag is used to label the created Service and passed as an argument to the HUG controller.
	// It follows the convention
	//  --hug-service-label=app.kubernetes.io/name:hug_<gateway_ns>_<gateway_name>,
	// where <gateway_ns> and <gateway_name> are replaced with the Gateway's namespace and name, respectively.
	// This allows the HUG controller to identify the correct Service for each Gateway
	// to report its LoadBalancer addresses in Gateway status.
	svcLabelVal := serviceLabelValue(gw.Namespace, gw.Name)
	hugSvcFlag := hugServiceLabelKey + ":" + svcLabelVal
	gwFlag := types.NamespacedName{Namespace: gw.Namespace, Name: gw.Name}.String()

	if err := r.reconcileDeployment(ctx, gw, resourceName, gwFlag, hugSvcFlag); err != nil {
		return err
	}
	return r.reconcileService(ctx, gw, resourceName, svcLabelVal)
}

// reconcileDeployment creates or updates the HUG controller Deployment.
func (r *GatewayReconciler) reconcileDeployment(ctx context.Context, gw *gatewayv1.Gateway, resourceName, gwFlag, hugSvcFlag string) error {
	desired := r.buildDeployment(resourceName, gw.Namespace, gw.Name, gwFlag, hugSvcFlag)

	existing := &appsv1.Deployment{}
	err := r.Get(ctx, types.NamespacedName{Namespace: r.Config.DeployerNs, Name: resourceName}, existing)
	if k8serrors.IsNotFound(err) {
		_, _ = fmt.Fprintf(os.Stderr, "deployer: creating deployment %s/%s\n", r.Config.DeployerNs, resourceName)
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(os.Stderr, "deployer: updating deployment %s/%s\n", r.Config.DeployerNs, resourceName)
	existing.Spec.Template.Spec.Containers[0].Args = desired.Spec.Template.Spec.Containers[0].Args
	existing.Spec.Template.Spec.Containers[0].Image = desired.Spec.Template.Spec.Containers[0].Image
	return r.Update(ctx, existing)
}

// reconcileService creates the HUG service if it does not already exist.
func (r *GatewayReconciler) reconcileService(ctx context.Context, gw *gatewayv1.Gateway, resourceName, svcLabelVal string) error {
	existing := &corev1.Service{}
	err := r.Get(ctx, types.NamespacedName{Namespace: r.Config.DeployerNs, Name: resourceName}, existing)
	if k8serrors.IsNotFound(err) {
		_, _ = fmt.Fprintf(os.Stderr, "deployer: creating service %s/%s\n", r.Config.DeployerNs, resourceName)
		return r.Create(ctx, r.buildService(resourceName, gw.Namespace, gw.Name, svcLabelVal))
	}
	return err
}

// deleteResources removes the Deployment and Service created for a Gateway.
func (r *GatewayReconciler) deleteResources(ctx context.Context, gwNS, gwName string) error {
	name := resourceName(gwNS, gwName)
	ns := r.Config.DeployerNs

	deploy := &appsv1.Deployment{}
	if err := r.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, deploy); err == nil {
		if err := r.Delete(ctx, deploy); err != nil && !k8serrors.IsNotFound(err) {
			return err
		}
	}

	svc := &corev1.Service{}
	if err := r.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, svc); err == nil {
		if err := r.Delete(ctx, svc); err != nil && !k8serrors.IsNotFound(err) {
			return err
		}
	}

	return nil
}

// buildDeployment returns the desired Deployment for a Gateway.
func (r *GatewayReconciler) buildDeployment(resourceName, gwNS, gwName, gatewayFlag, hugSvcFlag string) *appsv1.Deployment {
	one := int32(1)
	runLabel := map[string]string{"run": resourceName}
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      resourceName,
			Namespace: r.Config.DeployerNs,
			Labels: map[string]string{
				// Mirroring the "run" label from the Deployment
				"run": resourceName,
				// The watched Gateway's namespace and name are labeled to allow the HUG controller to correlate the Deployment with the correct Gateway.
				labelGatewayNSKey:   gwNS,
				labelGatewayNameKey: gwName,
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &one,
			Selector: &metav1.LabelSelector{MatchLabels: runLabel},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: runLabel},
				Spec: corev1.PodSpec{
					ServiceAccountName: "haproxy-unified-gateway",
					Containers: []corev1.Container{
						{
							Name:            "haproxy-unified-gateway",
							Image:           r.Config.ControllerImage,
							ImagePullPolicy: corev1.PullIfNotPresent,
							Args: []string{
								"--hugconf-crd=" + r.Config.HugConfCRD,
								"--log-type=text",
								"--gateway-ns-name=" + gatewayFlag,
								"--hug-service-label=" + hugSvcFlag,
							},
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									corev1.ResourceMemory: resource.MustParse("2560Mi"),
								},
								Requests: corev1.ResourceList{
									corev1.ResourceMemory: resource.MustParse("2048Mi"),
								},
							},
							Env: []corev1.EnvVar{
								{Name: "POD_NAME", ValueFrom: &corev1.EnvVarSource{
									FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.name"},
								}},
								{Name: "POD_NAMESPACE", ValueFrom: &corev1.EnvVarSource{
									FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.namespace"},
								}},
								{Name: "POD_IP", ValueFrom: &corev1.EnvVarSource{
									FieldRef: &corev1.ObjectFieldSelector{FieldPath: "status.podIP"},
								}},
							},
							SecurityContext: &corev1.SecurityContext{
								RunAsNonRoot:             new(true),
								AllowPrivilegeEscalation: new(false),
								RunAsUser:                new(int64(1000)),
								RunAsGroup:               new(int64(1000)),
								Capabilities: &corev1.Capabilities{
									Drop: []corev1.Capability{"ALL"},
									Add:  []corev1.Capability{"NET_BIND_SERVICE"},
								},
								SeccompProfile: &corev1.SeccompProfile{
									Type: corev1.SeccompProfileTypeRuntimeDefault,
								},
							},
							Ports: []corev1.ContainerPort{
								{Name: "stat", ContainerPort: 31024},
								{Name: "metrics", ContainerPort: 31060},
							},
						},
					},
				},
			},
		},
	}
}

// buildService returns the desired Service for a Gateway.
func (r *GatewayReconciler) buildService(resourceName, gwNS, gwName, svcLabelVal string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      resourceName,
			Namespace: r.Config.DeployerNs,
			Labels: map[string]string{
				hugServiceLabelKey: svcLabelVal,
				// The watched Gateway's namespace and name are labeled to the HUG Service
				labelGatewayNSKey:   gwNS,
				labelGatewayNameKey: gwName,
			},
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"run": resourceName},
			Type:     r.Config.ServiceType,
			Ports: []corev1.ServicePort{
				{Name: "stat", Port: 31024, TargetPort: intstr.FromInt32(31024)},
				{Name: "metrics", Port: 31060, TargetPort: intstr.FromInt32(31060)},
			},
		},
	}
}

// resourceName returns the Kubernetes name for the Deployment/Service created
// for a Gateway. A truncated SHA-256 hash of "ns/name" keeps the result
// well under the 63-character DNS label limit regardless of input length.
func resourceName(gwNS, gwName string) string {
	h := sha256.Sum256([]byte(gwNS + "/" + gwName))
	return fmt.Sprintf("hug-%x", h[:8])
}

// serviceLabelValue returns the value of the app.kubernetes.io/name label
// placed on the Service. Underscores are used to distinguish this from the
// resource name and match the --hug-service-label convention.
func serviceLabelValue(gwNS, gwName string) string {
	return resourceName(gwNS, gwName)
}

// ScaleDefaultControllerToZero scales the standard HUG controller deployment
// (label run=haproxy-unified-gateway in namespace haproxy-unified-gateway) to
// zero replicas. It returns a function that restores the deployment to one replica
// using the provided client, so callers can supply a fresh client independent of
// the manager lifecycle.
// apiReader bypasses the manager cache so the unwatch namespace can be accessed.
func ScaleDefaultControllerToZero(ctx context.Context, apiReader client.Reader, c client.Client) (func(context.Context, client.Client), error) {
	list := &appsv1.DeploymentList{}
	if err := apiReader.List(ctx, list,
		client.InNamespace(defaultControllerNS),
		client.MatchingLabels{"run": defaultControllerLabel},
	); err != nil {
		return nil, fmt.Errorf("listing default controller: %w", err)
	}

	_, _ = fmt.Fprintf(os.Stderr, "deployer: found %d default controller deployments to scale down\n", len(list.Items))
	zero := int32(0)
	one := int32(1)
	for i := range list.Items {
		patch := client.MergeFrom(list.Items[i].DeepCopy())
		list.Items[i].Spec.Replicas = &zero
		if err := c.Patch(ctx, &list.Items[i], patch); err != nil {
			return nil, fmt.Errorf("scaling down %s: %w", list.Items[i].Name, err)
		}
	}

	return func(restoreCtx context.Context, restoreClient client.Client) {
		for _, d := range list.Items {
			deploy := &appsv1.Deployment{}
			if err := apiReader.Get(restoreCtx, types.NamespacedName{Namespace: d.Namespace, Name: d.Name}, deploy); err != nil {
				ctrl.Log.Error(err, "getting controller deployment for restore", "name", d.Name)
				continue
			}
			patch := client.MergeFrom(deploy.DeepCopy())
			deploy.Spec.Replicas = &one
			if err := restoreClient.Patch(restoreCtx, deploy, patch); err != nil {
				ctrl.Log.Error(err, "restoring controller deployment", "name", d.Name)
			}
		}
	}, nil
}

// WaitForCleanup blocks until all deployer-managed Deployments and Services in
// deployerNs for Gateways from gatewayNS have been deleted, or ctx expires.
func WaitForCleanup(ctx context.Context, restCfg *rest.Config, deployerNs, gatewayNS string) error {
	s := runtime.NewScheme()
	utilruntime.Must(appsv1.AddToScheme(s))
	utilruntime.Must(corev1.AddToScheme(s))

	c, err := client.New(restCfg, client.Options{Scheme: s})
	if err != nil {
		return fmt.Errorf("creating client for cleanup wait: %w", err)
	}

	for {
		deploys := &appsv1.DeploymentList{}
		if err := c.List(ctx, deploys,
			client.InNamespace(deployerNs),
			client.MatchingLabels{labelGatewayNSKey: gatewayNS},
		); err != nil {
			return err
		}
		svcs := &corev1.ServiceList{}
		if err := c.List(ctx, svcs,
			client.InNamespace(deployerNs),
			client.MatchingLabels{labelGatewayNSKey: gatewayNS},
		); err != nil {
			return err
		}
		if len(deploys.Items) == 0 && len(svcs.Items) == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}
