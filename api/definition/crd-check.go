// Copyright 2023 HAProxy Technologies LLC
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
package definition

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Masterminds/semver/v3"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsclientset "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	api_error "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/yaml"
)

func homeDir() string {
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	return os.Getenv("USERPROFILE") // windows
}

func restConfig(external bool) (restConfig *rest.Config, err error) { //revive:disable:flag-parameter
	if external {
		kubeconfig := filepath.Join(homeDir(), ".kube", "config")
		restConfig, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	} else {
		restConfig, err = rest.InClusterConfig()
	}
	if err != nil {
		return restConfig, err
	}
	return restConfig, err
}

// CRDRefresh refreshes the CRDs from the configuration file.
// If the CRD does not exist, it creates it.
// If the CRD exists, it checks if it needs to be upgraded.
// If the CRD needs to be upgraded, it upgrades the CRD.
// The function prints the name of the CRD, if it exists, if it needs to be upgraded and if it was upgraded.
func CRDRefresh(external bool) error { //revive:disable:function-length
	fmt.Print(hugCRDUpdater)
	fmt.Println() // yes, linter complains during tests, wow
	fmt.Println("checking CRDs")
	config, err := restConfig(external)
	if err != nil {
		return err
	}

	// Create a new clientset for the apiextensions API group
	clientset, err := apiextensionsclientset.NewForConfig(config)
	if err != nil {
		return err
	}

	// Check if the CRD exists
	crds := getCRDs()
	for _, crdDef := range crds {
		// CustomResourceDefinition object
		var crd apiextensionsv1.CustomResourceDefinition
		err = yaml.Unmarshal(crdDef, &crd)
		if err != nil {
			return err
		}
		crdName := crd.Name
		fmt.Println()
		fmt.Println("checking CRD ", crdName)

		existingVersion, err := clientset.ApiextensionsV1().CustomResourceDefinitions().Get(context.Background(), crdName, metav1.GetOptions{})
		if err != nil {
			if !api_error.IsNotFound(err) {
				return err
			}
			fmt.Println("CRD " + crdName + " does not exist")
			// Create the CRD
			_, err = clientset.ApiextensionsV1().CustomResourceDefinitions().Create(context.Background(), &crd, metav1.CreateOptions{})
			if err != nil {
				return err
			}
			fmt.Println("CRD " + crdName + " created")
			continue
		}
		fmt.Println("CRD " + crdName + " exists")
		versions := existingVersion.Spec.Versions
		if len(versions) < 1 {
			fmt.Println("CRD ", crdName, " is empty ?")
			continue
		}
		// check if we have v1 and newest CN version
		crd.ObjectMeta.ResourceVersion = existingVersion.ObjectMeta.ResourceVersion
		if versions[0].Name == "v3" {
			cnInK8s, ok := getVersion(existingVersion.ObjectMeta.Annotations)

			needUpgrade := false
			if !ok {
				needUpgrade = true
			}
			cnNew, _ := getVersion(crd.ObjectMeta.Annotations)
			vK8s, err := semver.NewVersion(cnInK8s)
			if err != nil {
				needUpgrade = true
				fmt.Println(err.Error())
			}
			vNew, err := semver.NewVersion(cnNew)
			if err != nil {
				needUpgrade = true
				fmt.Println(err.Error())
			}
			if vNew == nil {
				vNew, _ = semver.NewVersion("0.0.0")
			}
			if vK8s == nil {
				vK8s, _ = semver.NewVersion("0.0.0")
			}
			fmt.Println("CRD", crdName, "exists as v3, [v"+vK8s.String()+"]")
			if needUpgrade || vNew.GreaterThan(vK8s) {
				// Upgrade the CRDl
				_, err = clientset.ApiextensionsV1().CustomResourceDefinitions().Update(context.Background(), &crd, metav1.UpdateOptions{})
				if err != nil {
					return err
				}
				fmt.Printf("CRD %s updated, [v%s] -> [v%s]\n", crdName, vK8s.String(), vNew.String())
			}
			continue
		}
		_, err = clientset.ApiextensionsV1().CustomResourceDefinitions().Update(context.Background(), &crd, metav1.UpdateOptions{})
		if err != nil {
			return err
		}
		fmt.Println("CRD", crdName, "updated")
	}

	fmt.Println("")
	fmt.Println("CRD update done")
	return nil
}

func getVersion(annotations map[string]string) (version string, ok bool) {
	version, ok = annotations["client-native.haproxy.org/version"]
	if ok {
		return version, ok
	}
	version, ok = annotations["gate.hug/version"]
	if ok {
		return version, ok
	}
	version, ok = annotations["conf.hug/version"]
	return version, ok
}

// hugCRDUpdater console pretty print
const hugCRDUpdater = `
  ____ ____  ____    _   _           _       _
 / ___|  _ \|  _ \  | | | |_ __   __| | __ _| |_ ___ _ __
| |   | |_) | | | | | | | | '_ \ / _` + "`" + ` |/ _` + "`" + ` | __/ _ \ '__|
| |___|  _ <| |_| | | |_| | |_) | (_| | (_| | ||  __/ |
 \____|_| \_\____/   \___/| .__/ \__,_|\__,_|\__\___|_|
                          |_|

`
