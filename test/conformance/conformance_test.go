//go:build conformance

// Copyright 2026 HAProxy Technologies LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package conformance_test

import (
	"io/fs"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	clientset "k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/yaml"

	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	"sigs.k8s.io/gateway-api/apis/v1alpha2"
	"sigs.k8s.io/gateway-api/apis/v1alpha3"
	"sigs.k8s.io/gateway-api/apis/v1beta1"
	"sigs.k8s.io/gateway-api/conformance"
	confv1 "sigs.k8s.io/gateway-api/conformance/apis/v1"
	"sigs.k8s.io/gateway-api/conformance/tests"
	conformanceconfig "sigs.k8s.io/gateway-api/conformance/utils/config"
	"sigs.k8s.io/gateway-api/conformance/utils/flags"
	"sigs.k8s.io/gateway-api/conformance/utils/suite"
	"sigs.k8s.io/gateway-api/pkg/features"
)

const (
	showDebug = true
)

var (
	gwClassName = envOrDefault("HUG_GATEWAY_CLASS", "haproxy")
	httpPort    = envOrDefault("HUG_HTTP_PORT", "31080")
	httpsPort   = envOrDefault("HUG_HTTPS_PORT", "31443")
)

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func TestConformance(t *testing.T) {
	log.SetLogger(zap.New(zap.WriteTo(os.Stdout), zap.UseDevMode(true)))

	cfg, err := config.GetConfig()
	require.NoError(t, err, "error loading Kubernetes config")

	c, err := client.New(cfg, client.Options{})
	require.NoError(t, err, "error initializing Kubernetes client")

	cs, err := clientset.NewForConfig(cfg)
	require.NoError(t, err, "error initializing Kubernetes clientset")

	require.NoError(t, gatewayv1.Install(c.Scheme()))
	require.NoError(t, v1beta1.Install(c.Scheme()))
	require.NoError(t, v1alpha2.Install(c.Scheme()))
	require.NoError(t, v1alpha3.Install(c.Scheme()))
	require.NoError(t, apiextensionsv1.AddToScheme(c.Scheme()))

	supportedFeatures := sets.New[features.FeatureName](
		features.SupportGateway,
		features.SupportHTTPRoute,
		features.SupportReferenceGrant,
	)

	conformanceProfiles := sets.New[suite.ConformanceProfileName](
		suite.GatewayHTTPConformanceProfileName,
	)

	if os.Getenv("HUG_TEST_TLS") == "1" {
		conformanceProfiles.Insert(suite.GatewayTLSConformanceProfileName)
		supportedFeatures.Insert(features.SupportTLSRoute)
	}

	reportOutput := envOrDefault("CONFORMANCE_REPORT_OUTPUT", "conformance-report.yaml")

	opts := suite.ConformanceOptions{
		Client:               c,
		Clientset:            cs,
		RestConfig:           cfg,
		GatewayClassName:     gwClassName,
		Debug:                showDebug,
		CleanupBaseResources: true,
		ManifestFS:           []fs.FS{&conformance.Manifests},
		SupportedFeatures:    supportedFeatures,
		ConformanceProfiles:  conformanceProfiles,
		TimeoutConfig:        conformanceconfig.DefaultTimeoutConfig(),
		AllowCRDsMismatch:    true,
		SkipProvisionalTests: true,
		Implementation: confv1.Implementation{
			Organization: "haproxytech",
			Project:      "haproxy-unified-gateway",
			URL:          "https://github.com/haproxytech/haproxy-unified-gateway",
			Version:      "dev",
			Contact:      []string{"https://github.com/haproxytech/haproxy-unified-gateway/issues"},
		},
		RunTest: *flags.RunTest,
	}

	// Build the suite ourselves instead of using RunConformanceWithOptions,
	// so we can register a t.Cleanup that always writes the report —
	// even when Setup() or Run() fail via require/t.FailNow().
	cSuite, err := suite.NewConformanceTestSuite(opts)
	require.NoError(t, err, "error initializing conformance suite")

	// Always write the report, even on failure.
	// t.Cleanup runs after t.FailNow(), so the report captures whatever
	// progress was made (partial results, setup failures, etc.).
	t.Cleanup(func() {
		report, err := cSuite.Report()
		if err != nil {
			t.Logf("error generating conformance report: %v", err)
			return
		}
		rawReport, err := yaml.Marshal(report)
		if err != nil {
			t.Logf("error marshaling conformance report: %v", err)
			return
		}
		if err := os.WriteFile(reportOutput, rawReport, 0o600); err != nil {
			t.Logf("error writing conformance report: %v", err)
			return
		}
		t.Logf("conformance report written to %s", reportOutput)
		t.Logf("Conformance report:\n%s", string(rawReport))
	})

	cSuite.Setup(t, tests.ConformanceTests)
	_ = cSuite.Run(t, tests.ConformanceTests)
}
