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
package deployer

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"
	clientfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// gwAPICompatEnv returns the GWAPI_COMPAT value; ok is false when absent.
func gwAPICompatEnv(env []corev1.EnvVar) (string, bool) {
	for _, e := range env {
		if e.Name == "GWAPI_COMPAT" {
			return e.Value, true
		}
	}
	return "", false
}

// Conformance needs 500 responses for invalid backends, so compat must not be configurable here.
func TestBuildDeploymentAlwaysSetsGWAPICompat(t *testing.T) {
	r := &GatewayReconciler{Config: Config{
		DeployerNs:       "haproxy-unified-gateway",
		ControllerImage:  "haproxytech/haproxy-unified-gateway:latest",
		HugConfCRD:       "haproxy-unified-gateway/hugconf",
		GatewayClassName: "haproxy",
	}}
	deploy := r.buildDeployment("res", "gateway-conformance-infra", "gw", "gateway-conformance-infra/gw", "app.kubernetes.io/name:val")

	value, ok := gwAPICompatEnv(deploy.Spec.Template.Spec.Containers[0].Env)
	require.True(t, ok, "generated controller containers must set GWAPI_COMPAT")
	require.Equal(t, "1", value)

	// The API server fills in the field-selector version, so the desired
	// spec must match or every reconcile sends a no-op update.
	for _, env := range deploy.Spec.Template.Spec.Containers[0].Env {
		if env.ValueFrom == nil || env.ValueFrom.FieldRef == nil {
			continue
		}
		require.Equal(t, "v1", env.ValueFrom.FieldRef.APIVersion, "field ref %s must declare its api version", env.Name)
	}
}

// Deployments left over from earlier runs must be healed to compat mode.
func TestReconcileDeploymentHealsGWAPICompat(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, appsv1.AddToScheme(scheme))

	r := &GatewayReconciler{Config: Config{
		DeployerNs:      "haproxy-unified-gateway",
		ControllerImage: "haproxytech/haproxy-unified-gateway:latest",
		HugConfCRD:      "haproxy-unified-gateway/hugconf",
	}}

	name := resourceName("gateway-conformance-infra", "gw")
	stale := &appsv1.Deployment{
		Name: name, Namespace: r.Config.DeployerNs,
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "haproxy-unified-gateway", Image: r.Config.ControllerImage}},
				},
			},
		},
	}
	c := clientfake.NewClientBuilder().WithScheme(scheme).WithObjects(stale).Build()
	r.Client = c

	gw := &gatewayv1.Gateway{}
	gw.Namespace = "gateway-conformance-infra"
	gw.Name = "gw"
	require.NoError(t, r.reconcileDeployment(context.Background(), gw, name, "gateway-conformance-infra/gw", "app.kubernetes.io/name:val"))

	got := &appsv1.Deployment{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Namespace: r.Config.DeployerNs, Name: name}, got))
	value, ok := gwAPICompatEnv(got.Spec.Template.Spec.Containers[0].Env)
	require.True(t, ok, "reconciled deployment must set GWAPI_COMPAT")
	require.Equal(t, "1", value)
}

// The GatewayNameMaximumLength conformance test uses a 253-byte gateway name,
// which must not exceed the 63-byte label value limit on created resources.
func TestGatewayNameLabelValue(t *testing.T) {
	require.Equal(t, "gw", gatewayNameLabelValue("gw"))
	require.Equal(t, strings.Repeat("a", 63), gatewayNameLabelValue(strings.Repeat("a", 63)))
	require.Equal(t, strings.Repeat("a", 63), gatewayNameLabelValue(strings.Repeat("a", 253)))

	// A cut may land on a separator; the value must stay a valid label.
	require.Equal(t, strings.Repeat("a", 62), gatewayNameLabelValue(strings.Repeat("a", 62)+"-"+strings.Repeat("b", 200)))

	r := &GatewayReconciler{Config: Config{
		DeployerNs:      "haproxy-unified-gateway",
		ControllerImage: "haproxytech/haproxy-unified-gateway:latest",
		HugConfCRD:      "haproxy-unified-gateway/hugconf",
	}}
	gwNS := "gateway-conformance-infra"
	gwName := "gateway-name-maximum-length-" + strings.Repeat("a", 253-28)
	require.Len(t, gwName, 253)
	res := resourceName(gwNS, gwName)

	deploy := r.buildDeployment(res, gwNS, gwName, gwNS+"/"+gwName, hugServiceLabelKey+":"+res)
	svc := r.buildService(res, gwNS, gwName, res)
	for _, labels := range []map[string]string{deploy.Labels, svc.Labels} {
		for k, v := range labels {
			require.Empty(t, validation.IsValidLabelValue(v), "label %s=%q", k, v)
		}
	}
	require.Equal(t, gwName[:63], deploy.Labels[labelGatewayNameKey])
	require.Equal(t, gwNS, svc.Labels[labelGatewayNSKey])
}
