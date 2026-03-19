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

// generate-gwapi reads the GWAPI_VERSIONS environment variable and regenerates
// all files that depend on the list of supported Gateway API versions:
//   - hug/jobs/gwapi/embed.go
//   - .gitlab/unit-tests.yml
//   - example/deploy/crd-update/job-gwapi.yaml
//   - test/integration/gatewayclass/manifests/dynamic-installedversions/expectations/conditions-ko.yaml
//
// It also downloads any missing .gz files and removes stale ones.
package main

import (
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

func downloadGzipped(url, dest string) error {
	resp, err := http.Get(url) //nolint:gosec,noctx
	if err != nil {
		return fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: status %s", url, resp.Status)
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	gw := gzip.NewWriter(f)
	if _, err := io.Copy(gw, resp.Body); err != nil {
		return fmt.Errorf("writing %s: %w", dest, err)
	}
	return gw.Close()
}

type version struct {
	Tag          string // e.g. "v1.3.0"
	VarName      string // e.g. "v130"
	Short        string // e.g. "1.3.0"
	CI           string // e.g. "1_3_0"
	Minor        string // e.g. "v1.3"
	Experimental bool   // true if listed in GWAPI_EXP_VERSIONS
}

func main() {
	raw := os.Getenv("GWAPI_VERSIONS")
	if raw == "" {
		fmt.Fprintln(os.Stderr, "GWAPI_VERSIONS environment variable is not set")
		os.Exit(1)
	}

	// Parse experimental versions into a set for quick lookup.
	expSet := make(map[string]bool)
	for s := range strings.SplitSeq(os.Getenv("GWAPI_EXP_VERSIONS"), ",") {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if !strings.HasPrefix(s, "v") {
			s = "v" + s
		}
		expSet[s] = true
	}

	var versions []version
	for s := range strings.SplitSeq(raw, ",") {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		tag := s
		if !strings.HasPrefix(tag, "v") {
			tag = "v" + tag
		}
		short := strings.TrimPrefix(tag, "v")
		parts := strings.SplitN(short, ".", 3)
		varName := "v" + parts[0] + parts[1] + parts[2]
		ci := strings.ReplaceAll(short, ".", "_")
		minor := "v" + parts[0] + "." + parts[1]
		versions = append(versions, version{
			Tag:          tag,
			VarName:      varName,
			Short:        short,
			CI:           ci,
			Minor:        minor,
			Experimental: expSet[tag],
		})
	}

	if len(versions) == 0 {
		fmt.Fprintln(os.Stderr, "no versions parsed from GWAPI_VERSIONS")
		os.Exit(1)
	}

	gwapiDir := "hug/jobs/gwapi"

	// Download missing .gz files and remove stale ones
	downloadAndClean(gwapiDir, versions)

	// Generate files
	generate("hug/jobs/gwapi/embed.go", embedGoTmpl, versions)
	generate(".gitlab/unit-tests.yml", gitlabCITmpl, versions)
	generate("example/deploy/crd-update/job-gwapi.yaml", jobGWAPITmpl, versions)
	generate("test/integration/gatewayclass/manifests/dynamic-installedversions/expectations/conditions-ko.yaml", conditionsKOTmpl, versions)
}

func generate(path, tmplStr string, versions []version) {
	tmpl, err := template.New(path).Parse(tmplStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "template parse error for %s: %v\n", path, err)
		os.Exit(1)
	}
	f, err := os.Create(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create %s: %v\n", path, err)
		os.Exit(1)
	}
	defer f.Close()
	if err := tmpl.Execute(f, versions); err != nil {
		fmt.Fprintf(os.Stderr, "template execute error for %s: %v\n", path, err)
		os.Exit(1)
	}
	fmt.Printf("  generated %s\n", path)
}

func downloadAndClean(dir string, versions []version) {
	want := make(map[string]bool)
	for _, v := range versions {
		gz := filepath.Join(dir, v.Tag+"-experimental.yaml.gz")
		want[gz] = true
		if _, err := os.Stat(gz); err == nil {
			continue
		}
		fmt.Printf("  downloading %s experimental CRDs...\n", v.Tag)
		url := fmt.Sprintf("https://github.com/kubernetes-sigs/gateway-api/releases/download/%s/experimental-install.yaml", v.Tag)
		if err := downloadGzipped(url, gz); err != nil {
			fmt.Fprintf(os.Stderr, "download %s failed: %v\n", v.Tag, err)
			os.Exit(1)
		}
		fmt.Printf("  downloaded %s\n", gz)
	}
	// Remove stale .gz files.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), "-experimental.yaml.gz") {
			full := filepath.Join(dir, e.Name())
			if !want[full] {
				fmt.Printf("  removing stale %s\n", e.Name())
				os.Remove(full)
			}
		}
	}
}

var embedGoTmpl = `// Copyright 2026 HAProxy Technologies LLC
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

// Code generated by cmd/generate-gwapi; DO NOT EDIT.

package gwapi

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Masterminds/semver/v3"
)
{{range .}}
//go:embed {{.Tag}}-experimental.yaml.gz
var {{.VarName}} []byte
{{end}}
// SupportedVersions lists all embedded Gateway API versions.
var SupportedVersions = []string{ {{- range $i, $v := .}}{{if $i}}, {{end}}"{{$v.Tag}}"{{end -}} }

// versions maps semver version strings to their embedded gzipped CRD YAML.
var versions = map[string][]byte{
{{- range .}}
	"{{.Tag}}": {{.VarName}},
{{- end}}
}

// BundleVersionAnnotation is the annotation key used by Gateway API CRDs
// to indicate the bundle version.
const BundleVersionAnnotation = "gateway.networking.k8s.io/bundle-version"

// Get returns the embedded experimental Gateway API CRD YAML for the given version.
// The version can be specified with or without the "v" prefix (e.g., "1.3.0" or "v1.3.0").
// The embedded data is gzip-compressed and decompressed on the fly.
func Get(version string) ([]byte, error) {
	v, err := semver.NewVersion(version)
	if err != nil {
		return nil, fmt.Errorf("invalid version %q: %w", version, err)
	}
	key := "v" + v.String()
	compressed, ok := versions[key]
	if !ok {
		return nil, fmt.Errorf("unsupported Gateway API version %q, supported: %v", key, SupportedVersions)
	}
	return decompress(compressed)
}

// WriteCRDsToDir decompresses the embedded GWAPI CRDs for the given version
// and writes each YAML document as a separate file in dir.
// This is useful for envtest which needs a directory of individual CRD files.
func WriteCRDsToDir(version, dir string) error {
	data, err := Get(version)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", dir, err)
	}
	for i, part := range bytes.Split(data, []byte("\n---")) {
		trimmed := bytes.TrimSpace(part)
		if len(trimmed) == 0 {
			continue
		}
		name := filepath.Join(dir, fmt.Sprintf("gwapi-%02d.yaml", i))
		if err := os.WriteFile(name, trimmed, 0o644); err != nil {
			return fmt.Errorf("failed to write %s: %w", name, err)
		}
	}
	return nil
}

func decompress(data []byte) ([]byte, error) {
	r, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer r.Close()
	return io.ReadAll(r)
}
`

var gitlabCITmpl = `# Code generated by cmd/generate-gwapi; DO NOT EDIT.
{{range .}}
tests-GW-API-{{.CI}}:
{{- if .Experimental}}
  allow_failure: true
{{- end}}
  retry: 2
  needs: ["diff", "tidy"]
  rules:
    - if: $CI_PIPELINE_SOURCE == 'merge_request_event'
    - if: "$CI_PROJECT_NAMESPACE != 'haproxy-controller/community' && $CI_PIPELINE_SOURCE == 'push'"
  stage: unit-tests
  image:
    name: $HAPROXY_REGISTRY_GO/haproxy-alpine:$HAPROXY_VERSION-go$GO_VERSION
    entrypoint: [""]
  tags:
    - go
  variables:
    METRICS_OUTPUT_DIR: ${CI_PROJECT_DIR}/metrics-output/gwapi-{{.CI}}
  before_script:
    - export PATH=$PATH:/root/go/bin
    - mkdir -p $METRICS_OUTPUT_DIR
  script:
    - GWAPI_VERSION={{.Short}} task test
  artifacts:
    when: always
    paths:
      - junit-report.xml
      - metrics-output/gwapi-{{.CI}}
    reports:
      junit: junit-report.xml
{{end -}}
`

var conditionsKOTmpl = `# Code generated by cmd/generate-gwapi; DO NOT EDIT.
- message: GatewayClass is accepted
  reason: Accepted
  status: "True"
  type: Accepted
  observedGeneration: 1
- message: Gateway API CRD versions are not supported. Please install version {{- range $i, $v := .}}{{if $i}},{{end}} {{$v.Minor}}{{end}}
  reason: UnsupportedVersion
  status: "False"
  type: SupportedVersion
  observedGeneration: 1
`

var jobGWAPITmpl = `# Code generated by cmd/generate-gwapi; DO NOT EDIT.
apiVersion: batch/v1
kind: Job
metadata:
  name: haproxy-unified-gateway-gwapi # job name must be unique for each run
  namespace: haproxy-unified-gateway
spec:
  template:
    spec:
      serviceAccountName: haproxy-unified-gateway-crd
      containers:
      - name: haproxy-unified-gateway-gwapi
        image: haproxytech/haproxy-unified-gateway:latest
        # imagePullPolicy: this is set to never for kind cluster usage
        # imagePullPolicy: Always
        imagePullPolicy: Never
        command: ["/usr/local/sbin/hug","--job-gwapi={{(index . 0).Short}}"]
      restartPolicy: Never
  backoffLimit: 0
`
