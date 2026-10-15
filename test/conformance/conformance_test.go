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
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/haproxytech/haproxy-unified-gateway/test/conformance/deployer"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
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
	// ClusterIP for CI where conformance tests run in kind cluster without a load-balancer.
	// LoadBalancer for local testing (where LoadBalancer services typically get an external IP via MetalLB or cloud-provider-kind).
	hugSvcType = envOrDefault("HUG_SERVICE_TYPE", string(corev1.ServiceTypeLoadBalancer))
)

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// implementationFromEnv builds the report's implementation block. The version is
// environment-driven so a released tag can be reported honestly without editing
// this file; the remaining fields are fixed per release.
func implementationFromEnv() confv1.Implementation {
	return confv1.Implementation{
		Organization: "HAProxy Technologies",
		Project:      "haproxy-unified-gateway",
		URL:          "https://github.com/haproxytech/haproxy-unified-gateway",
		Version:      envOrDefault("HUG_IMPLEMENTATION_VERSION", "dev"),
		Contact:      []string{"https://github.com/haproxytech/haproxy-unified-gateway/issues"},
	}
}

// controllerImageFromEnv returns the HUG image the per-Gateway controllers run.
// For a release report this is the published image for that tag, so the report
// describes exactly the artifact that shipped.
func controllerImageFromEnv() string {
	return fmt.Sprintf("haproxytech/haproxy-unified-gateway:%s",
		envOrDefault("HUG_CONTROLLER_IMAGE_TAG", "latest"))
}

func TestConformance(t *testing.T) {
	fmt.Fprintln(os.Stderr, "conformance: test starting")
	log.SetLogger(zap.New(zap.WriteTo(os.Stderr), zap.UseDevMode(true)))

	cfg, err := config.GetConfig()
	require.NoError(t, err, "error loading Kubernetes config")
	fmt.Fprintln(os.Stderr, "conformance: k8s config loaded")

	c, err := client.New(cfg, client.Options{})
	require.NoError(t, err, "error initializing Kubernetes client")

	cs, err := clientset.NewForConfig(cfg)
	require.NoError(t, err, "error initializing Kubernetes clientset")
	fmt.Fprintln(os.Stderr, "conformance: k8s clients ready")

	require.NoError(t, gatewayv1.Install(c.Scheme()))
	require.NoError(t, v1beta1.Install(c.Scheme()))
	require.NoError(t, v1alpha2.Install(c.Scheme()))
	require.NoError(t, v1alpha3.Install(c.Scheme()))
	require.NoError(t, apiextensionsv1.AddToScheme(c.Scheme()))

	supportedFeatures := []features.FeatureName{
		features.SupportGateway,
		features.SupportHTTPRoute,
		features.SupportReferenceGrant,
		features.SupportGatewayAddressEmpty,
		features.SupportGatewayHTTPListenerIsolation,
		features.SupportGatewayPort8080,
		// Extended: HTTPRoute method and query-param matching, enabled by the
		// same conditional-candidate machinery as header matching.
		features.SupportHTTPRouteMethodMatching,
		features.SupportHTTPRouteQueryParamMatching,
		// Extended: filters translated into http-request rules.
		features.SupportHTTPRouteResponseHeaderModification,
		features.SupportHTTPRoutePathRewrite,
		features.SupportHTTPRouteHostRewrite,
		features.SupportHTTPRoutePathRedirect,
		features.SupportHTTPRoutePortRedirect,
		features.SupportHTTPRouteSchemeRedirect,
		// Extended: per-backendRef header modification.
		features.SupportHTTPRouteBackendRequestHeaderModification,
		// Extended: CORS policy and parentRef listener-port selection.
		features.SupportHTTPRouteCORS,
		features.SupportHTTPRouteParentRefPort,
		features.SupportHTTPRouteDestinationPortMatching,
		// Extended: named rules route exactly like unnamed ones.
		features.SupportHTTPRouteNamedRouteRule,
	}

	conformanceProfiles := []suite.ConformanceProfileName{
		suite.GatewayHTTPConformanceProfileName,
		suite.GatewayTLSConformanceProfileName,
	}

	// ListenerSet is a feature the HTTP and TLS profiles carry, not a profile of its own, so
	// declaring it is what enrolls its tests. It stays opt-in until the conformance campaign has
	// been run against it: what is declared here is also what the published report claims as
	// supported.
	if os.Getenv("HUG_TEST_LISTENERSET") == "1" {
		supportedFeatures = append(supportedFeatures, features.SupportListenerSet)
	}

	reportOutput := envOrDefault("CONFORMANCE_REPORT_OUTPUT", "conformance-report.yaml")
	if os.Getenv("CONFORMANCE_REPORT_OUTPUT") == "" {
		if _, err := os.Stat(reportOutput); err == nil {
			// A campaign takes minutes and its report is the only record of it: an unset path must
			// not let a later run overwrite an earlier one. A caller that pins the path -- the
			// taskfile, CI -- keeps its name, and owns the overwrite it asked for.
			previous := reportOutput
			reportOutput = fmt.Sprintf("conformance-report-%s.yaml", time.Now().Format("20060102T150405"))
			fmt.Fprintf(os.Stderr, "conformance: %s exists, writing %s instead\n", previous, reportOutput)
		}
	}

	nodeIP := getNodeIP(context.Background(), cs)
	t.Logf("NodeIP %s", nodeIP)

	// v1.6 moved the CLI flags into ConfigurableOptions; ApplyAll only
	// copies flags explicitly set on the command line, so the registry
	// defaults must be restored by hand.
	var cfgOpts suite.ConfigurableOptions
	flags.ApplyAll(&cfgOpts)

	opts := suite.ConformanceOptions{
		ConfigurableOptions: cfgOpts,
		Client:              c,
		Clientset:           cs,
		RestConfig:          cfg,
		ManifestFS:          []fs.FS{&conformance.Manifests},
	}
	opts.Implementation = implementationFromEnv()
	opts.GatewayClassName = gwClassName
	opts.Debug = showDebug
	// Upstream default: test manifests are deleted after each test. Without
	// this, routes and gateways pile up across the suite: stale catch-all
	// routes break 404 expectations and port conflicts break condition tests.
	opts.CleanupTestResources = true
	opts.CleanupBaseResources = true
	opts.SupportedFeatures = supportedFeatures
	opts.ConformanceProfiles = conformanceProfiles
	opts.TimeoutConfig = conformanceconfig.DefaultTimeoutConfig()
	opts.AllowCRDsMismatch = true
	opts.SkipProvisionalTests = true
	// v1.6 marks 36 tests Parallel and runs them concurrently. This harness cannot: it
	// serves one namespace and deploys a controller per Gateway, so every new Gateway
	// leaves its neighbours' whole-namespace readiness check (NamespacesMustBeReady)
	// failing for as long as a pod takes to start. Sequential by default;
	// HUG_TEST_PARALLEL=1 opts back into the concurrent mode, to measure what it costs.
	opts.DisableParallelTests = os.Getenv("HUG_TEST_PARALLEL") != "1"

	t.Logf("conformance run test %s", opts.RunTest)
	t.Logf("conformance skip tests %s", opts.SkipTests)

	// Build the suite ourselves instead of using RunConformanceWithOptions,
	// so we can register a t.Cleanup that always writes the report —
	// even when Setup() or Run() fail via require/t.FailNow().
	fmt.Fprintln(os.Stderr, "conformance: calling NewConformanceTestSuite")
	cSuite, err := suite.NewConformanceTestSuite(opts)
	require.NoError(t, err, "error initializing conformance suite")
	fmt.Fprintln(os.Stderr, "conformance: suite created")

	// Start the deployer and wait for its cache to sync before running any
	// conformance tests, so Gateway objects created by the suite are
	// immediately visible to the reconciler.
	ctx, cancel := context.WithCancel(context.Background())

	deployerDone, restoreDone, err := deployer.Start(ctx, cfg, deployer.Config{
		DeployerNs:       "haproxy-unified-gateway",
		ControllerImage:  controllerImageFromEnv(),
		HugConfCRD:       "haproxy-unified-gateway/hugconf",
		GatewayClassName: gwClassName,
		WatchNamespaces:  []string{"gateway-conformance-infra", "haproxy-unified-gateway"},
		ServiceType:      corev1.ServiceType(hugSvcType),
	})
	require.NoError(t, err, "starting gateway deployer")
	fmt.Fprintln(os.Stderr, "conformance: deployer started, waiting for scale-down signal")
	require.NoError(t, <-deployerDone, "scaling down default controller")
	fmt.Fprintln(os.Stderr, "conformance: scale-down complete, running suite setup")

	// Always write the report and stop the deployer on test completion, even on
	// failure. t.Cleanup runs after t.FailNow(), so the report captures whatever
	// progress was made (partial results, setup failures, etc.).
	t.Cleanup(func() {
		waitCtx, waitCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer waitCancel()
		if err := deployer.WaitForCleanup(waitCtx, cfg, "haproxy-unified-gateway", "gateway-conformance-infra"); err != nil {
			t.Logf("waiting for deployer cleanup: %v", err)
		}
		cancel()
		<-restoreDone
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
		// The report is the only record of a campaign, and a caller may name a path
		// whose directory it expected something else to have created. Failing to write
		// because of a missing directory would lose the run, so create it here: a
		// campaign that already failed is exactly when the report matters most.
		if err := os.MkdirAll(filepath.Dir(reportOutput), 0o755); err != nil {
			t.Logf("error creating the conformance report directory: %v", err)
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
