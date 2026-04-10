//
// Copyright 2025 HAProxy Technologies LLC
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

package utils // revive:disable:var-naming

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer" // Standard Kubernetes scheme
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type RuntimeYamlParams struct {
	Ctx               context.Context
	CrtlruntimeClient ctrlruntimeclient.Client
	Dir               string
	Namespace         string
	ManifestNames     []string
	WaitForResult     bool // Flag to wait for object to be created/deleted
}

const (
	timeout  = time.Second * 10
	interval = time.Second * 1
)

// CreateRuntimeObjectsFromYAMLFiles reads YAML files, decodes them into Kubernetes objects,
// and creates them using the provided client.
// It handles multi-document YAML files (separated by "---").
func CreateRuntimeObjectsFromYAMLFiles(params RuntimeYamlParams) error {
	logger := log.FromContext(params.Ctx)

	files, err := os.ReadDir(params.Dir)
	if err != nil {
		return err
	}
	gwAPIVersion := os.Getenv("GWAPI_VERSION")
	var filesSpecific []os.DirEntry
	if gwAPIVersion != "" {
		gwAPIVersion = "v" + gwAPIVersion
		var errSpec error
		filesSpecific, errSpec = os.ReadDir(path.Join(params.Dir, "gwapi_specific", gwAPIVersion))
		if errSpec != nil {
			_, pathError := errors.AsType[*fs.PathError](errSpec)
			if !pathError {
				return errSpec
			}
		}
	}
	fileList := map[string]string{}
	for _, filePath := range files {
		if filePath.IsDir() {
			continue // Skip directories
		}
		fullPath := filepath.Join(params.Dir, filePath.Name())
		fileList[filePath.Name()] = fullPath
	}
	for _, filePath := range filesSpecific {
		if filePath.IsDir() {
			continue // Skip directories
		}
		fullPathValue := filepath.Join(params.Dir, "gwapi_specific", gwAPIVersion, filePath.Name())
		fileList[filePath.Name()] = fullPathValue
	}

	mnames := map[string]struct{}{}
	for _, manifest := range params.ManifestNames {
		mnames[manifest] = struct{}{}
	}

	for fileName, filePath := range fileList {
		// if ManifestNames is not empty, then consider only those ones
		if len(mnames) > 0 {
			if _, ok := mnames[fileName]; !ok {
				continue
			}
		}

		logger.Info("Processing file", "path", fileName)

		manifestData, err := os.ReadFile(filePath)
		if err != nil {
			return err
		}
		manifests := bytes.SplitSeq(manifestData, []byte("\n---\n"))

		for manifest := range manifests {
			obj, gvk, err := RuntimeFromBytes(params.CrtlruntimeClient, manifest)
			if err != nil {
				return err
			}

			clientObj, ok := obj.(ctrlruntimeclient.Object)
			if !ok {
				err := fmt.Errorf("decoded object from embedded file is not a client.Object: %T (GVK: %s)", obj, gvk.String())
				logger.Error(err, "Type assertion failed", "path", filePath)
				return err
			}
			clientObj.SetNamespace(params.Namespace)
			err = CreateRuntimeObject(params.Ctx, params.CrtlruntimeClient, clientObj, params.WaitForResult)
			if err != nil {
				logger.Error(err, "Failed to create object in cluster", "details", ctrlruntimeclient.ObjectKeyFromObject(clientObj))
				return fmt.Errorf("failed to create object %s: %w", ctrlruntimeclient.ObjectKeyFromObject(clientObj), err)
			}
		}
	}
	return nil
}

//revive:disable
func CreateRuntimeObject(ctx context.Context, client ctrlruntimeclient.Client, obj ctrlruntimeclient.Object, waitForResult bool) error {
	//revive:enable
	logger := log.FromContext(ctx)
	var err error

	if err = client.Create(ctx, obj); err != nil {
		if apierrors.IsAlreadyExists(err) {
			// The object already exists, so we'll try to update it.
			// This is useful in tests where we might re-apply the same manifest.
			logger.Info("Object already exists, trying to update", "details", ctrlruntimeclient.ObjectKeyFromObject(obj))
			existingObj, ok := obj.DeepCopyObject().(ctrlruntimeclient.Object)
			if !ok {
				return fmt.Errorf("failed to copy object %s", ctrlruntimeclient.ObjectKeyFromObject(obj))
			}
			if getErr := client.Get(ctx, ctrlruntimeclient.ObjectKeyFromObject(obj), existingObj); getErr != nil {
				return fmt.Errorf("failed to get existing object for update %s: %w", ctrlruntimeclient.ObjectKeyFromObject(obj), getErr)
			}
			obj.SetResourceVersion(existingObj.GetResourceVersion())
			if updateErr := client.Update(ctx, obj); updateErr != nil {
				return fmt.Errorf("failed to update object %s: %w", ctrlruntimeclient.ObjectKeyFromObject(obj), updateErr)
			}
			return nil
		}
		logger.Error(err, "Failed to create object in cluster", "details", ctrlruntimeclient.ObjectKeyFromObject(obj))
		return fmt.Errorf("failed to create object %s: %w", ctrlruntimeclient.ObjectKeyFromObject(obj), err)
	}

	if waitForResult {
		gotObj, ok := obj.DeepCopyObject().(ctrlruntimeclient.Object)
		if !ok {
			return fmt.Errorf("failed to copy object %s", ctrlruntimeclient.ObjectKeyFromObject(obj))
		}
		errW := WaitFor(ctx, interval, timeout, func() bool {
			err := client.Get(
				ctx,
				ctrlruntimeclient.ObjectKeyFromObject(obj), gotObj)
			return !apierrors.IsNotFound(err)
		})
		if !errW {
			return fmt.Errorf("error waiting for the object to be created: [%s]/[%s]", ctrlruntimeclient.ObjectKeyFromObject(obj), obj.GetObjectKind().GroupVersionKind().Kind)
		}
	}

	return nil
}

//revive:disable
func DeleteRuntimeObject(ctx context.Context, client ctrlruntimeclient.Client, obj ctrlruntimeclient.Object, waitForResult bool) error {
	//revive:enable
	logger := log.FromContext(ctx)

	if err := client.Delete(ctx, obj); err != nil {
		logger.Error(err, "failed to delete object in cluster", "details", ctrlruntimeclient.ObjectKeyFromObject(obj))
		return fmt.Errorf("failed to delete object %s: %w", ctrlruntimeclient.ObjectKeyFromObject(obj), err)
	}

	if waitForResult {
		gotObj, ok := obj.DeepCopyObject().(ctrlruntimeclient.Object)
		if !ok {
			return fmt.Errorf("failed to copy object %s", ctrlruntimeclient.ObjectKeyFromObject(obj))
		}
		errW := WaitFor(ctx, interval, timeout, func() bool {
			err := client.Get(
				ctx,
				ctrlruntimeclient.ObjectKeyFromObject(obj), gotObj)
			return apierrors.IsNotFound(err) || gotObj.GetDeletionTimestamp() != nil
		})
		if !errW {
			return fmt.Errorf("error waiting for the object to be deleted: %s/%s", ctrlruntimeclient.ObjectKeyFromObject(gotObj), gotObj.GetObjectKind().GroupVersionKind().Kind)
		}
	}
	return nil
}

func DeleteRuntimeObjectsFromYAMLFiles(params RuntimeYamlParams) error {
	logger := log.FromContext(params.Ctx)

	files, err := os.ReadDir(params.Dir)
	if err != nil {
		return err
	}
	gwAPIVersion := os.Getenv("GWAPI_VERSION")
	var filesSpecific []os.DirEntry
	if gwAPIVersion != "" {
		gwAPIVersion = "v" + gwAPIVersion
		var errSpec error
		filesSpecific, errSpec = os.ReadDir(path.Join(params.Dir, "gwapi_specific", gwAPIVersion))
		if errSpec != nil {
			_, pathError := errors.AsType[*fs.PathError](errSpec)
			if !pathError {
				return errSpec
			}
		}
	}
	fileList := map[string]string{}
	for _, filePath := range files {
		if filePath.IsDir() {
			continue // Skip directories
		}
		fullPath := filepath.Join(params.Dir, filePath.Name())
		fileList[filePath.Name()] = fullPath
	}
	for _, filePath := range filesSpecific {
		if filePath.IsDir() {
			continue // Skip directories
		}
		fullPathValue := filepath.Join(params.Dir, "gwapi_specific", gwAPIVersion, filePath.Name())
		fileList[filePath.Name()] = fullPathValue
	}

	mnames := map[string]struct{}{}
	for _, manifest := range params.ManifestNames {
		mnames[manifest] = struct{}{}
	}

	for fileName, filePath := range fileList {
		// if ManifestNames is not empty, then consider only those ones
		if len(mnames) > 0 {
			if _, ok := mnames[fileName]; !ok {
				continue
			}
		}

		logger.Info("Processing file", "path", filePath)

		manifestData, err := os.ReadFile(filePath)
		if err != nil {
			return err
		}
		manifests := bytes.SplitSeq(manifestData, []byte("\n---\n"))

		for manifest := range manifests {
			obj, gvk, err := RuntimeFromBytes(params.CrtlruntimeClient, manifest)
			if err != nil {
				return err
			}

			clientObj, ok := obj.(ctrlruntimeclient.Object)
			if !ok {
				err := fmt.Errorf("decoded object from embedded file is not a client.Object: %T (GVK: %s)", obj, gvk.String())
				logger.Error(err, "Type assertion failed", "path", filePath)
				return err
			}
			clientObj.SetNamespace(params.Namespace)
			err = DeleteRuntimeObject(params.Ctx, params.CrtlruntimeClient, clientObj, params.WaitForResult)
			if err != nil {
				logger.Error(err, "Failed to delete object in cluster", "details", ctrlruntimeclient.ObjectKeyFromObject(clientObj))
				return fmt.Errorf("failed to delete object %s: %w", ctrlruntimeclient.ObjectKeyFromObject(clientObj), err)
			}
		}
	}
	return nil
}

// RuntimeFromBytes returns a list of Kubernetes runtime objects from their yaml templates.
func RuntimeFromBytes(ctrlclient ctrlruntimeclient.Client, manifest []byte) (runtime.Object, *schema.GroupVersionKind, error) {
	decode := serializer.NewCodecFactory(ctrlclient.Scheme()).UniversalDeserializer().Decode

	obj, gvk, err := decode(manifest, nil, nil)
	if err != nil {
		return nil, nil, err
	}

	return obj, gvk, nil
}
