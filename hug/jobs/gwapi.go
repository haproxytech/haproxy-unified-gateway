// Copyright 2025 HAProxy Technologies LLC
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
package jobs

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Masterminds/semver/v3"
	"github.com/haproxytech/haproxy-unified-gateway/hug/jobs/gwapi"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsclientset "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	api_error "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	k8syaml "sigs.k8s.io/yaml"
)

// GWAPIInstall installs or updates the Gateway API experimental CRDs for the
// given version. If the cluster already has a newer version installed, the
// operation is refused (no downgrade).
func GWAPIInstall(external bool, version string) error {
	fmt.Print(gwapiInstaller)

	data, err := gwapi.Get(version)
	if err != nil {
		return err
	}

	requestedVer, err := semver.NewVersion(version)
	if err != nil {
		return fmt.Errorf("invalid version %q: %w", version, err)
	}
	fmt.Printf("Installing Gateway API experimental CRDs v%s\n\n", requestedVer.String())

	config, err := getRestConfig(external)
	if err != nil {
		return err
	}

	clientset, err := apiextensionsclientset.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to create API extensions client: %w", err)
	}

	// Parse the multi-document YAML into individual CRD documents
	crds := splitYAMLDocuments(data)

	for _, rawCRD := range crds {
		var crd apiextensionsv1.CustomResourceDefinition
		if err := k8syaml.Unmarshal(rawCRD, &crd); err != nil {
			return fmt.Errorf("failed to unmarshal CRD: %w", err)
		}

		// Skip non-CRD documents (e.g. Namespace, ValidatingWebhookConfiguration)
		if crd.Kind != "CustomResourceDefinition" {
			continue
		}

		fmt.Printf("checking CRD %s\n", crd.Name)

		existing, err := clientset.ApiextensionsV1().CustomResourceDefinitions().Get(
			context.Background(), crd.Name, metav1.GetOptions{},
		)
		if err != nil {
			if !api_error.IsNotFound(err) {
				return fmt.Errorf("failed to get CRD %s: %w", crd.Name, err)
			}
			// CRD does not exist, create it
			_, err = clientset.ApiextensionsV1().CustomResourceDefinitions().Create(
				context.Background(), &crd, metav1.CreateOptions{},
			)
			if err != nil {
				return fmt.Errorf("failed to create CRD %s: %w", crd.Name, err)
			}
			fmt.Printf("  CRD %s created\n", crd.Name)
			continue
		}

		// CRD exists - check for downgrade
		existingBundleVer, ok := existing.Annotations[gwapi.BundleVersionAnnotation]
		if ok {
			existingVer, parseErr := semver.NewVersion(existingBundleVer)
			if parseErr == nil && existingVer.GreaterThan(requestedVer) {
				return fmt.Errorf(
					"refusing to downgrade Gateway API CRD %s from %s to v%s",
					crd.Name, existingBundleVer, requestedVer.String(),
				)
			}
		}

		// Update the CRD
		crd.ObjectMeta.ResourceVersion = existing.ObjectMeta.ResourceVersion
		_, err = clientset.ApiextensionsV1().CustomResourceDefinitions().Update(
			context.Background(), &crd, metav1.UpdateOptions{},
		)
		if err != nil {
			return fmt.Errorf("failed to update CRD %s: %w", crd.Name, err)
		}
		if existingBundleVer != "" {
			fmt.Printf("  CRD %s updated [%s] -> [v%s]\n", crd.Name, existingBundleVer, requestedVer.String())
		} else {
			fmt.Printf("  CRD %s updated\n", crd.Name)
		}
	}

	fmt.Println()
	fmt.Println("Gateway API CRD installation done")
	return nil
}

// splitYAMLDocuments splits a multi-document YAML byte slice into individual documents.
func splitYAMLDocuments(data []byte) [][]byte {
	var docs [][]byte
	for part := range bytes.SplitSeq(data, []byte("\n---")) {
		trimmed := bytes.TrimSpace(part)
		if len(trimmed) == 0 {
			continue
		}
		docs = append(docs, trimmed)
	}
	return docs
}

func homeDir() string {
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	return os.Getenv("USERPROFILE") // windows
}

//revive:disable-next-line:flag-parameter
func getRestConfig(external bool) (*rest.Config, error) {
	if external {
		kubeconfig := filepath.Join(homeDir(), ".kube", "config")
		return clientcmd.BuildConfigFromFlags("", kubeconfig)
	}
	return rest.InClusterConfig()
}

const gwapiInstaller = `
   ______          __    ___    ____  ____   ____           __        ____
  / ____/      __ / /   /   |  / __ \/  _/  /  _/___  _____/ /_____ _/ / /__  _____
 / / __| | /| / // /   / /| | / /_/ // /    / // __ \/ ___/ __/ __ ` + "`" + `/ / / _ \/ ___/
/ /_/ /| |/ |/ // /   / ___ |/ ____// /   _/ // / / (__  ) /_/ /_/ / / /  __/ /
\____/ |__/|__//_/   /_/  |_/_/   /___/  /___/_/ /_/____/\__/\__,_/_/_/\___/_/
`
