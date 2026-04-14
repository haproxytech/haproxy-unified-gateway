// Copyright 2019 HAProxy Technologies LLC
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

package base

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/go-cmp/cmp"
	rc "github.com/haproxytech/haproxy-unified-gateway/k8s/gate/conditions/routes"
	futils "github.com/haproxytech/haproxy-unified-gateway/k8s/gate/fileutils"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	"sigs.k8s.io/yaml"

	"github.com/haproxytech/client-native/v6/models"
	truntime "github.com/haproxytech/haproxy-unified-gateway/test/integration/runtime"
	"github.com/haproxytech/haproxy-unified-gateway/test/integration/utils"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/stretchr/testify/suite"
)

const (
	timeout               = time.Second * 15
	interval              = time.Second * 1
	TestMapThroughRuntime = true
)

type BaseSuite struct {
	suite.Suite
	metricsCancel context.CancelFunc
	metricsDone   chan struct{}
	test          IntTest
}

func (b *BaseSuite) Test() IntTest {
	return b.test
}

func (b *BaseSuite) SetupSuite(crdRelativePath string, levelsUp int) {
	var err error
	b.test, err = NewIntTest(b.T(), crdRelativePath, levelsUp)
	b.Require().NoError(err)

	b.test.StartTestEnv(b.T())
	b.startMetricsSampler()
}

func (b *BaseSuite) TearDownSuite() {
	b.stopMetricsSampler()
	b.test.StopTestEnv(b.T())
	b.test.StopHaproxy(b.T())
}

// metricsInterval returns the sampling interval from the METRICS_SAMPLE_INTERVAL
// environment variable (in seconds). Defaults to 2 seconds.
func metricsInterval() time.Duration {
	if v := os.Getenv("METRICS_SAMPLE_INTERVAL"); v != "" {
		if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
			return time.Duration(secs) * time.Second
		}
	}
	return 2 * time.Second
}

// metricsOutputDir returns the directory where metrics samples are written.
// Uses METRICS_OUTPUT_DIR if set, otherwise falls back to HaproxyCfgDir.
func (b *BaseSuite) metricsOutputDir() string {
	if dir := os.Getenv("METRICS_OUTPUT_DIR"); dir != "" {
		return dir
	}
	return b.test.HaproxyCfgDir
}

// metricsSample is a single timestamped snapshot of all metric values.
type metricsSample struct {
	Timestamp time.Time               `json:"ts"`
	Metrics   map[string]metricValues `json:"metrics"`
	Suite     string                  `json:"suite"`
	Test      string                  `json:"test"`
	Elapsed   float64                 `json:"elapsed_s"`
}

// metricValues holds the numeric values for a single metric family.
// For counters/gauges: Value is set. For histograms: Count, Sum, Buckets are set.
type metricValues struct {
	Type    string            `json:"type"`
	Labels  map[string]string `json:"labels,omitempty"`
	Value   *float64          `json:"value,omitempty"`
	Count   *uint64           `json:"count,omitempty"`
	Sum     *float64          `json:"sum,omitempty"`
	Buckets []histBucket      `json:"buckets,omitempty"`
}

type histBucket struct {
	UpperBound float64 `json:"le"`
	Count      uint64  `json:"count"`
}

// metricsSamplingEnabled returns true when METRICS_SAMPLE_ENABLED is set to "1".
// When not set (e.g. local dev), no sampling or file writing occurs.
func metricsSamplingEnabled() bool {
	return os.Getenv("METRICS_SAMPLE_ENABLED") == "1"
}

// startMetricsSampler starts a background goroutine that periodically gathers
// metrics from the controller-runtime registry and writes them as JSON lines.
func (b *BaseSuite) startMetricsSampler() {
	if !metricsSamplingEnabled() {
		return
	}

	outputDir := b.metricsOutputDir()
	if outputDir == "" {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	b.metricsCancel = cancel
	b.metricsDone = make(chan struct{})

	sampleInterval := metricsInterval()
	b.T().Logf("metrics sampler: interval=%v, output=%s", sampleInterval, outputDir)

	go func() {
		defer close(b.metricsDone)

		suiteName := b.T().Name()
		samplesFile := filepath.Join(outputDir, "hug_metrics_samples.jsonl")
		f, err := os.OpenFile(samplesFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return
		}
		defer f.Close()

		enc := json.NewEncoder(f)
		startTime := time.Now()
		ticker := time.NewTicker(sampleInterval)
		defer ticker.Stop()

		// Take an initial sample immediately
		b.writeSample(enc, suiteName, b.T().Name(), startTime)

		for {
			select {
			case <-ctx.Done():
				// Final sample on shutdown
				b.writeSample(enc, suiteName, b.T().Name(), startTime)
				return
			case <-ticker.C:
				// b.T().Name() is mutex-protected in testify and returns
				// the current subtest name (e.g. "TestSuite/TestMethod").
				b.writeSample(enc, suiteName, b.T().Name(), startTime)
			}
		}
	}()
}

func (*BaseSuite) writeSample(enc *json.Encoder, suiteName, testName string, startTime time.Time) {
	families, err := metrics.Registry.Gather()
	if err != nil {
		return
	}

	now := time.Now()
	sample := metricsSample{
		Suite:     suiteName,
		Test:      testName,
		Timestamp: now,
		Elapsed:   now.Sub(startTime).Seconds(),
		Metrics:   make(map[string]metricValues),
	}

	for _, mf := range families {
		for _, m := range mf.GetMetric() {
			name := mf.GetName()
			labels := labelsToMap(m.GetLabel())
			key := name
			if len(labels) > 0 {
				// Make key unique per label combination
				var parts []string
				for k, v := range labels {
					parts = append(parts, k+"="+v)
				}
				slices.Sort(parts)
				key = name + "{" + strings.Join(parts, ",") + "}"
			}

			mv := metricValues{Labels: labels}
			switch mf.GetType() {
			case dto.MetricType_COUNTER:
				mv.Type = "counter"
				v := m.GetCounter().GetValue()
				mv.Value = &v
			case dto.MetricType_GAUGE:
				mv.Type = "gauge"
				v := m.GetGauge().GetValue()
				mv.Value = &v
			case dto.MetricType_HISTOGRAM:
				mv.Type = "histogram"
				h := m.GetHistogram()
				c := h.GetSampleCount()
				s := h.GetSampleSum()
				mv.Count = &c
				mv.Sum = &s
				for _, b := range h.GetBucket() {
					mv.Buckets = append(mv.Buckets, histBucket{
						UpperBound: b.GetUpperBound(),
						Count:      b.GetCumulativeCount(),
					})
				}
			default:
				continue
			}

			sample.Metrics[key] = mv
		}
	}

	_ = enc.Encode(sample)
}

func labelsToMap(lps []*dto.LabelPair) map[string]string {
	if len(lps) == 0 {
		return nil
	}
	m := make(map[string]string, len(lps))
	for _, lp := range lps {
		m[lp.GetName()] = lp.GetValue()
	}
	return m
}

// stopMetricsSampler stops the background sampler and writes a final
// Prometheus text dump for quick human inspection.
func (b *BaseSuite) stopMetricsSampler() {
	if !metricsSamplingEnabled() {
		return
	}

	if b.metricsCancel != nil {
		b.metricsCancel()
		<-b.metricsDone
	}

	// Also write a final Prometheus text format snapshot
	b.dumpMetricsText()
}

// dumpMetricsText writes the current metrics state in Prometheus text format.
func (b *BaseSuite) dumpMetricsText() {
	t := b.T()
	families, err := metrics.Registry.Gather()
	if err != nil {
		t.Logf("DumpMetrics: failed to gather metrics: %v", err)
		return
	}

	var buf bytes.Buffer
	encoder := expfmt.NewEncoder(&buf, expfmt.NewFormat(expfmt.TypeTextPlain))
	for _, mf := range families {
		if err := encoder.Encode(mf); err != nil {
			t.Logf("DumpMetrics: failed to encode metric family %s: %v", mf.GetName(), err)
			continue
		}
	}

	// for debugging only
	// t.Logf("=== HUG Prometheus Metrics ===\n%s", buf.String())

	outputDir := b.metricsOutputDir()
	if outputDir != "" {
		metricsFile := filepath.Join(outputDir, "hug_metrics.txt")
		if err := os.WriteFile(metricsFile, buf.Bytes(), 0o644); err != nil {
			t.Logf("DumpMetrics: failed to write metrics file %s: %v", metricsFile, err)
		} else {
			t.Logf("DumpMetrics: metrics written to %s", metricsFile)
		}
	}
}

// CreateFixtures will create all the objects from manifests that are in the fixturePath directory
// They are created in the test namespace
// To create in a specific namespace use CreateFixturesInNamespace
// If manifestNames is empty, it will create all objects that are in the directory
// If manifestNames, it will create only the objects in manifestNames (a sublist of the files in fixturePath)
func (b *BaseSuite) CreateFixtures(fixturePath string, manifestNames []string) {
	params := utils.RuntimeYamlParams{
		Ctx:               b.Test().Ctx,
		CrtlruntimeClient: b.Test().Client,
		Namespace:         b.Test().Namespace,
		Dir:               fixturePath,
		WaitForResult:     true,
		ManifestNames:     manifestNames,
	}
	err := utils.CreateRuntimeObjectsFromYAMLFiles(params)
	b.Require().NoError(err)
}

// CleanupFixturesCheckMapFiles is similar to CleanupFixtures, but it takes an additional
// argument mapFileRelativePath, which is the path to the map file that should
// be cleaned up. This is useful for tests that create a map file and then
// need to clean it up after the test has finished.
//
// Note that CleanupFixturesCheckMapFiles will wait until the map file is empty before
// returning. This is to ensure that the test does not finish before the cleanup
// has finished.
func (b *BaseSuite) CleanupFixturesCheckMapFiles(fixturePath string, manifestNames []string, mapFileRelativePaths []string) {
	b.T().Logf("Cleaning up fixtures in %s", fixturePath)
	defer b.T().Logf("End of cleaning up fixtures in %s", fixturePath)
	params := utils.RuntimeYamlParams{
		Ctx:               b.Test().Ctx,
		CrtlruntimeClient: b.Test().Client,
		Namespace:         b.Test().Namespace,
		Dir:               fixturePath,
		WaitForResult:     true,
		ManifestNames:     manifestNames,
	}
	err := utils.DeleteRuntimeObjectsFromYAMLFiles(params)
	b.Require().NoError(err)
	for _, mapFileRelativePath := range mapFileRelativePaths {
		b.ExpectMapContents(mapFileRelativePath, "")
	}
}

func (b *BaseSuite) CreateFixturesInNamespace(fixturePath, namespace string, manifestNames []string) {
	params := utils.RuntimeYamlParams{
		Ctx:               b.Test().Ctx,
		CrtlruntimeClient: b.Test().Client,
		Namespace:         namespace,
		Dir:               fixturePath,
		WaitForResult:     true,
		ManifestNames:     manifestNames,
	}
	err := utils.CreateRuntimeObjectsFromYAMLFiles(params)
	b.Require().NoError(err)
}

func (b *BaseSuite) CleanupFixtures(fixturePath string, manifestNames []string) {
	params := utils.RuntimeYamlParams{
		Ctx:               b.Test().Ctx,
		CrtlruntimeClient: b.Test().Client,
		Namespace:         b.Test().Namespace,
		Dir:               fixturePath,
		WaitForResult:     true,
		ManifestNames:     manifestNames,
	}
	err := utils.DeleteRuntimeObjectsFromYAMLFiles(params)
	b.Require().NoError(err)
}

func (b *BaseSuite) CleanupFixturesInNamespace(fixturePath, namespace string, manifestNames []string) {
	params := utils.RuntimeYamlParams{
		Ctx:               b.Test().Ctx,
		CrtlruntimeClient: b.Test().Client,
		Namespace:         namespace,
		Dir:               fixturePath,
		WaitForResult:     true,
		ManifestNames:     manifestNames,
	}
	err := utils.DeleteRuntimeObjectsFromYAMLFiles(params)
	b.Require().NoError(err)
}

// Return HAProxy master process if it exists.
// func haproxyProcess(pidFile string) (*os.Process, error) {
// 	file, err := os.Open(pidFile)
// 	if err != nil {
// 		return nil, err
// 	}
// 	defer file.Close()
// 	scanner := bufio.NewScanner(file)
// 	scanner.Scan()
// 	pid, err := strconv.Atoi(scanner.Text())
// 	if err != nil {
// 		return nil, err
// 	}
// 	process, err := os.FindProcess(pid)
// 	if err != nil {
// 		return nil, err
// 	}
// 	err = process.Signal(syscall.Signal(0))
// 	return process, err
// }

// For now: only checks the following certificate fields:
// - StorageName
// - Subject (for CN)
// Check is performed using the RUNTIME command on haproxy
func (b *BaseSuite) ExpectCertificates(ctx context.Context, expectedCerts []*models.SslCertificate) {
	if !utils.WaitFor(ctx, interval, timeout, func() bool {
		// Runtime command to get the list of certs
		// Warning: the returned certs are not filled completely, only the StorageName and Description
		certs, err := b.Test().RuntimeClient.ShowCerts()
		if err != nil {
			return false
		}

		if len(expectedCerts) != len(certs) {
			fmt.Printf("len(expectedCerts): %v\n", len(expectedCerts))
			fmt.Printf("len(certs): %v\n", len(certs))
			return false
		}
		gotCertSkeletonMap := make(map[string]*models.SslCertificate)
		for _, cert := range certs {
			gotCertSkeletonMap[cert.StorageName] = cert
		}

		for _, expectedCert := range expectedCerts {
			if _, ok := gotCertSkeletonMap[expectedCert.StorageName]; !ok {
				return false
			}
			// Runtime command to actually get the Cert content
			gotCert, err := b.Test().RuntimeClient.ShowCertificate(expectedCert.StorageName)
			b.Require().NoError(err)
			areOK := b.areCertsEqual(expectedCert, gotCert)
			if !areOK {
				return false
			}
		}
		return true
	}) {
		b.T().Fatal("certificates not correct")
	}
}

// For now: only checks the following certificate fields:
// - StorageName
// - Subject (for CN)
func (*BaseSuite) areCertsEqual(expected, got *models.SslCertificate) bool {
	// Do they have the same CN ?
	return expected.StorageName == got.StorageName && expected.Subject == got.Subject
}

// ExpectCrtLists checks that the crt-list are the expectedCrtLists
// For now, there is no CN method to get the content of the crt-list : "show ssl crt-list <filename>"
// The only existing method in CN is "show ssl crt-list" that gives the list of crt-lists
// So, for now, we check the content of the crt-list from the crt-list file, not from the RUNTIME.
func (b *BaseSuite) ExpectCrtLists(ctx context.Context, expectedCrtLists map[futils.FilePath][]string) {
	if !utils.WaitFor(ctx, interval, timeout, func() bool {
		// Runtime command to get the list of crtLists
		crtLists, err := b.Test().RuntimeClient.ShowCrtLists()
		if err != nil {
			return false
		}

		if len(expectedCrtLists) != len(crtLists) {
			return false
		}
		gotCrtListSkeletonMap := make(map[string]*models.SslCrtList)
		for _, crtList := range crtLists {
			gotCrtListSkeletonMap[crtList.File] = crtList
		}

		for crtListFilePath, expectedCrtListContent := range expectedCrtLists {
			if _, ok := gotCrtListSkeletonMap[crtListFilePath.FullPath()]; !ok {
				return false
			}
			gotContent, err := crtListFilePath.ReadLines()
			b.Require().NoError(err)
			slices.Sort(gotContent)
			// Runtime command to actually get the CrtList content
			areEqual := b.areCrtListContentEqual(expectedCrtListContent, gotContent)
			if !areEqual {
				return false
			}
		}
		return true
	}) {
		b.T().Fatal("crt-list not correct")
	}
}

func (*BaseSuite) areCrtListContentEqual(expected, got []string) bool {
	if len(expected) != len(got) {
		return false
	}
	for i := range expected {
		if expected[i] != got[i] {
			return false
		}
	}
	return true
}

func (b *BaseSuite) ExpectFrontends(ctx context.Context, expectationPath string, expectedFrontends []string) {
	var diffs string
	if !utils.WaitFor(ctx, interval, timeout, func() bool {
		frontends, err := b.test.HaproxyClient.FrontendsGet()
		if err != nil {
			return false
		}

		gotFrontends := make(map[string]*models.Frontend)
		for _, fe := range frontends {
			gotFrontends[fe.Name] = fe
			// b.exportFrontend(fe)
		}

		for _, expectedFeName := range expectedFrontends {
			var gotFrontend *models.Frontend
			var ok bool
			if gotFrontend, ok = gotFrontends[expectedFeName]; !ok {
				return false
			}

			expectedFrontend := b.FrontendFromManifest(expectationPath, expectedFeName)
			areSame := expectedFrontend.Equal(*gotFrontend)
			if !areSame {
				diffs = cmp.Diff(expectedFrontend, *gotFrontend)
				return false
			}
		}
		return true
	}) {
		b.T().Fatalf("frontends diffs\n %v ", diffs)
	}
}

func (b *BaseSuite) FrontendFromManifest(manifestPath, manifestName string) *models.Frontend {
	mpath := path.Join(manifestPath, manifestName+".yaml")
	yamlFile, err := os.ReadFile(mpath)
	b.Require().NoError(err)

	var fe models.Frontend
	err = yaml.Unmarshal(yamlFile, &fe)
	b.Require().NoError(err)
	return &fe
}

func (b *BaseSuite) ExpectBackends(ctx context.Context, expectationPath string, expectedBackends []string) {
	var diffs string
	if !utils.WaitFor(ctx, interval, timeout, func() bool {
		backends, err := b.test.HaproxyClient.BackendsGet()
		if err != nil {
			return false
		}

		gotBackends := make(map[string]*models.Backend)
		for _, be := range backends {
			gotBackends[be.Name] = be
			// b.exportBackend(be)
		}

		for _, expectedBeName := range expectedBackends {
			var gotBackend *models.Backend
			var ok bool
			if gotBackend, ok = gotBackends[expectedBeName]; !ok {
				return false
			}

			expectedBackend := b.BackendFromManifest(expectationPath, expectedBeName)
			areSame := expectedBackend.Equal(*gotBackend)
			if !areSame {
				diffs = cmp.Diff(expectedBackend, *gotBackend)
				return false
			}
		}
		return true
	}) {
		b.T().Fatalf("backends diffs\n %v ", diffs)
	}
}

func (b *BaseSuite) ExpectBackendsDoNotExist(ctx context.Context, backendThatShouldNotExist string) {
	var diffs map[string][]any
	if !utils.WaitFor(ctx, interval, timeout, func() bool {
		backends, err := b.test.HaproxyClient.BackendsGet()
		if err != nil {
			return false
		}

		gotBackends := make(map[string]*models.Backend)
		for _, be := range backends {
			gotBackends[be.Name] = be
		}

		if _, ok := gotBackends[backendThatShouldNotExist]; !ok {
			return true
		}

		return false
	}) {
		b.T().Fatalf("backends diffs\n %v ", diffs)
	}
}

func (b *BaseSuite) BackendFromManifest(manifestPath, manifestName string) *models.Backend {
	mpath := path.Join(manifestPath, manifestName+".yaml")
	yamlFile, err := os.ReadFile(mpath)
	b.Require().NoError(err)

	var be models.Backend
	err = yaml.Unmarshal(yamlFile, &be)
	b.Require().NoError(err)
	return &be
}

func (b *BaseSuite) GetMapFileFrom(mapFileRelativePath string) ([]string, error) {
	var mapFile []byte
	mapFile, err := os.ReadFile(filepath.Join(b.test.HaproxyCfgDir, "maps", mapFileRelativePath))
	if err != nil {
		return nil, err
	}
	return strings.Split(string(mapFile), "\n"), nil
}

func (b *BaseSuite) CheckEntryInMapFile(mapFileRelativePath, key, value string) bool {
	mapFile, err := b.GetMapFileFrom(mapFileRelativePath)
	if err != nil {
		return false
	}
	return slices.Contains(mapFile, key+" "+value)
}

var StandardMaps = []string{
	"domain_wildcard_sni.map",
	"listener_exact_match.map",
	"listener_route_exact_match.map",
	"listener_route_wildcard_match.map",
	"listener_wildcard_match.map",
	"path_exact.map",
	"path_prefix.map",
	"path_regex.map",
	"sni.map",
}

func (b *BaseSuite) ExpectMapContents(mapFilePath, expectedMapPath string) {
	b.Require().Eventually(func() bool {
		// Standard maps (maps in expectedMapPath/mapFilePath)
		check := b.checkMapContents(mapFilePath, expectedMapPath)
		return check
	}, timeout, interval, fmt.Sprintf("maps in %s/%s did not match expected contents", expectedMapPath, mapFilePath))
}

// ExpectListenerRouteMapContents is a no-op: listener maps are now per-frontend
// and are verified by ExpectMapContents for each frontend directory.
func (*BaseSuite) ExpectListenerRouteMapContents(_ string) {}

func (b *BaseSuite) checkMapContents(mapFileRelativePath, expectedMapPath string) bool {
	checkFile := b.checkMapFileContents(mapFileRelativePath, expectedMapPath)
	if !checkFile {
		return false
	}
	if TestMapThroughRuntime {
		return b.checkRuntimeMapContents(mapFileRelativePath, expectedMapPath)
	}
	return true
}

func (b *BaseSuite) checkMapFileContents(mapFileRelativePath, expectedMapPath string) bool {
	var mapOK bool
	// Standard maps
	b.T().Logf("Checking [standard] map [file] %s/%s", expectedMapPath, mapFileRelativePath)

	for _, mapName := range StandardMaps {
		b.T().Logf(" Checking map [file] %s", mapName)
		mapOK = b.check1MapContent(mapFileRelativePath, expectedMapPath, mapName)
		if !mapOK {
			return false
		}
	}
	return true
}

func (b *BaseSuite) check1MapContent(mapFileRelativePath, expectedMapPath, mapName string) bool {
	// For Route mapping (maps in expectedMapPath)
	expectedFilePath := path.Join(expectedMapPath, mapFileRelativePath, mapName)

	// Check if expectation exists
	expectedContent, err := os.ReadFile(expectedFilePath)
	expectationExists := err == nil

	// Read actual map
	actualMapPath := filepath.Join(b.test.HaproxyCfgDir, "maps", mapFileRelativePath, mapName)

	actualContent, err := os.ReadFile(actualMapPath)
	// If actual map doesn't exist, we treat it as empty string
	var actualString string
	if err == nil {
		actualString = string(actualContent)
	}

	if expectationExists {
		// Check if content matches
		if string(expectedContent) != actualString {
			b.T().Logf("  map [file] mismatch for %s: \nexpected %q, \ngot      %q", mapName, string(expectedContent), actualString)
			return false
		}
	} else {
		// Check if actual is empty
		if strings.TrimSpace(actualString) != "" {
			b.T().Logf("   map [file] %s should be empty but has content: %q", mapName, actualString)
			return false
		}
	}
	return true
}

func (b *BaseSuite) ExpectRouteConditionsUpdated(ctx context.Context, namespace, name string, expectedConditions rc.RouteConditions) {
	route := &gatewayv1.HTTPRoute{}
	var gotConditions rc.RouteConditions
	if !utils.WaitFor(ctx, interval, timeout, func() bool {
		if err := b.Test().Client.Get(
			b.Test().Ctx,
			types.NamespacedName{Name: name, Namespace: namespace}, route); err != nil {
			return false
		}

		gotConditions = rc.NewRouteConditionsFromV1RouteConditions(route.Status.Parents, TestControllerName)

		res := gotConditions.Equal(expectedConditions)

		return res
	}) {
		b.T().Fatalf("conditions not correct,\nGot %+v\nExpected %+v\n", gotConditions, expectedConditions)
	}
}

func (b *BaseSuite) ExpectAttachedRoute(ctx context.Context, namespace, gwName, listenerName string, expectNbAttachedRoutes int32) {
	gw := &gatewayv1.Gateway{}
	if !utils.WaitFor(ctx, interval, timeout, func() bool {
		if err := b.Test().Client.Get(
			b.Test().Ctx,
			types.NamespacedName{Name: gwName, Namespace: namespace}, gw); err != nil {
			return false
		}

		for _, listenerStatus := range gw.Status.Listeners {
			if string(listenerStatus.Name) == listenerName {
				return listenerStatus.AttachedRoutes == expectNbAttachedRoutes
			}
		}

		return false
	}) {
		b.T().Fatal("AttachedRoutes not correct")
	}
}

func (b *BaseSuite) ExpectServers(backend string, expectedServers []string) {
	var servers []string
	res := b.Eventually(func() bool {
		var err error
		servers, err = truntime.GetServers(b.test.RuntimeSocketPath, backend)
		if err != nil {
			return false
		}
		slices.Sort(servers)
		slices.Sort(expectedServers)
		return slices.Equal(servers, expectedServers)
	}, timeout, interval, fmt.Sprintf("servers in backend %s did not match expected", backend))
	if !res {
		msg := fmt.Sprintf("servers in backend %s did not match expected. Got %v, expected %v", backend, servers, expectedServers)
		b.T().Fatal(msg)
	}
}

// WaitForNoReloadsAnyMore checks during an overall overAllDuration
// That we reach a stable state without reloads for at least consistentlyDurationWithoutReloads
// We set a timer stabilityTimer and if at any point during the stability window a reload occurs, we reset this timer.
// If we reach the overAllDuration without a stable window without reload we issue a t.Fatalf()
func (b *BaseSuite) WaitForNoReloadsAnyMore(
	overAllDuration time.Duration,
	consistentlyDurationWithoutReloads time.Duration,
) string {
	fmt.Printf("\n...wait for a stability window without reloads: %v\n", consistentlyDurationWithoutReloads)

	info, err := truntime.GetGlobalHAProxyInfo(b.test.RuntimeSocketPath)
	pid := info.Pid
	if err != nil {
		b.T().Fatalf("error getting HAProxy info: %v", err)
	}
	overallTimeout := time.After(overAllDuration)
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	// stabilityTimer is reset each time there is a reload
	// It's a sliding window
	// We want to have a stable state without reloads for a least consistentlyDurationWithoutReloads
	var stabilityTimer *time.Timer
	inStabilityWindow := false

	shouldContinue := true
	for shouldContinue {
		select {
		case <-overallTimeout:
			b.T().Fatalf("Timed out after %v without reaching stable state without reloads", overAllDuration)
		case <-ticker.C:
			reloaded, newPid, err := b.haproxyReloadHappened(pid)
			if err != nil {
				b.T().Log(err)
				continue
			}
			pid = newPid
			if reloaded {
				// reload happened; if we were counting stability, reset.
				if inStabilityWindow {
					stabilityTimer.Stop()
					inStabilityWindow = false
					fmt.Print("reload happened; stopping stability timer...\n")
				}
				continue
			}
			// no reload
			if !inStabilityWindow {
				// start the stability countdown
				inStabilityWindow = true
				stabilityTimer = time.NewTimer(consistentlyDurationWithoutReloads)
				fmt.Print("no reload; starting stability timer...\n")
				continue
			}
			// already in stability window, check if time is up
			select {
			case <-stabilityTimer.C:
				// SUCCESS: stable for the full duration
				shouldContinue = false
				fmt.Printf("no reload in %v... stable state reached...\n", consistentlyDurationWithoutReloads)
			default:
				// still waiting for stability
			}
		}
	}
	return pid
	// Exit the loop means that we were stable without reload for consistentlyDurationWithoutReloads duration
}

// haproxyReloadHappened returns:
// - a bool true if a reload did happen
// - the new pid
// - an error if we could get the worker pid
// The detection of reload is based on the check that the worker pid did change or not
func (b *BaseSuite) haproxyReloadHappened(oldPid string) (bool, string, error) {
	newPid, err := truntime.GetGlobalHAProxyInfo(b.test.RuntimeSocketPath)
	if err != nil {
		b.T().Log(err)
		return false, "", err
	}
	fmt.Printf("oldPid/newPid: %s/%s\n", oldPid, newPid.Pid)
	return newPid.Pid != oldPid, newPid.Pid, nil
}

// ConsistentlyNoReload executes a check repeatedly for a duration.
// It t.Fatalf() if the condition ever returns false.
func (b *BaseSuite) ConsistentlyNoReload(oldPid string, duration time.Duration) {
	fmt.Printf("\n...check consistent no reload during a window: %v\n", duration)
	t := b.T()
	t.Helper()

	deadline := time.After(duration)
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-deadline:
			// The entire duration passed without the condition failing
			return
		case <-ticker.C:
			// Check the condition
			reloadHappened, _, err := b.haproxyReloadHappened(oldPid)
			if err == nil {
				if reloadHappened {
					t.Fatal("FAIL: some reload happened")
					return
				}
			} else {
				t.Error("FAILED to get pid")
			}
		}
	}
}

func (b *BaseSuite) checkRuntimeMapContents(mapFileRelativePath, expectedMapPath string) bool {
	b.T().Logf("Checking map [runtime] for %s/%s ", expectedMapPath, mapFileRelativePath)

	for _, mapName := range StandardMaps {
		b.T().Logf(" Checking map [runtime] %s", mapName)

		chek := b.check1RuntimeMapContent(mapFileRelativePath, expectedMapPath, mapName)
		if !chek {
			return false
		}
	}

	return true
}

func (b *BaseSuite) check1RuntimeMapContent(mapFileRelativePath, expectedMapPath, mapName string) bool {
	socketPath := filepath.Join(b.test.HaproxyCfgDir, "haproxy-runtime-api.sock")

	expectedFilePath := path.Join(expectedMapPath, mapFileRelativePath, mapName)

	// Check if expectation exists
	expectedContent, err := os.ReadFile(expectedFilePath)
	expectationExists := err == nil

	// Build runtime map path (must match HAProxy config path exactly)
	runtimeMapPath := filepath.Join(
		b.test.HaproxyCfgDir,
		"maps",
		mapFileRelativePath,
		mapName,
	)

	// Read map from runtime socket
	actualString, err := readRuntimeMap(socketPath, runtimeMapPath)
	if err != nil {
		// If map not found in runtime, treat it as empty
		actualString = ""
	}

	expectedNormalized := normalizeMapContent(string(expectedContent))
	actualNormalized := normalizeMapContent(actualString)
	if strings.HasPrefix(actualNormalized, "Unknown map identifier") {
		actualNormalized = ""
	}

	if expectationExists {
		b.T().Logf("map [runtime] contents: %s", actualNormalized)
		b.T().Logf("Expected map [runtime] contents: %s", expectedNormalized)
		if expectedNormalized != actualNormalized {
			b.T().Logf(
				"  map [runtime] mismatch for %s:\nexpected:\n%q\ngot:\n%q",
				mapName,
				expectedNormalized,
				actualNormalized,
			)
			return false
		}
	} else {
		if strings.TrimSpace(actualNormalized) != "" {
			b.T().Logf(
				"   map [runtime] %s should be empty but has content: %q",
				mapName,
				actualNormalized,
			)
			return false
		}
	}
	return true
}

func readRuntimeMap(socketPath, mapPath string) (string, error) {
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return "", err
	}
	defer conn.Close()

	cmd := fmt.Sprintf("show map %s\n", mapPath)
	if _, err := conn.Write([]byte(cmd)); err != nil {
		return "", err
	}

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, conn); err != nil {
		return "", err
	}

	output := buf.String()

	if strings.Contains(output, "No such map") {
		return "", fmt.Errorf("map %s not found", mapPath)
	}

	return output, nil
}

func normalizeMapContent(content string) string {
	lines := strings.Split(content, "\n")
	var cleaned []string

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		fields := strings.Fields(line)

		// Remove runtime memory address if present
		if len(fields) > 1 && strings.HasPrefix(fields[0], "0x") {
			fields = fields[1:]
		}

		cleaned = append(cleaned, strings.Join(fields, " "))
	}

	slices.Sort(cleaned)

	return strings.Join(cleaned, "\n")
}
