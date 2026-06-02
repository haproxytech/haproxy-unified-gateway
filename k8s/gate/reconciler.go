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

package controller

import (
	"context"
	"fmt"
	"log/slog"
	"reflect"

	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/events"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/logging"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NamespacedNameFilterFunc is a function that returns true if the resource should be processed by the reconciler.
// If the function returns false, the reconciler will log the returned string.
type NamespacedNameFilterFunc func(nsname types.NamespacedName) (shouldProcess bool, msg string)

// ReconcilerConfig is the configuration for the reconciler.
type ReconcilerConfig struct {
	// Getter gets a resource from the k8s API.
	Getter Getter
	// ObjectType is the type of the resource that the reconciler will reconcile.
	ObjectType client.Object
	// EventCh is the channel where the reconciler will send events.
	EventCh chan<- any
	// NamespacedNameFilter filters resources the controller will process. Can be nil.
	NamespacedNameFilter NamespacedNameFilterFunc
	Logger               *slog.Logger
	OnlyMetadata         bool
}

// Reconciler reconciles Kubernetes resources of a specific type.
// It implements the reconcile.Reconciler interface.
// A successful reconciliation of a resource has the two possible outcomes:
// (1) If the resource is deleted, the Implementation will send a DeleteEvent to the event channel.
// (2) If the resource is upserted (created or updated), the Implementation will send an UpsertEvent
// to the event channel.
type Reconciler struct {
	cfg ReconcilerConfig
}

var _ reconcile.Reconciler = &Reconciler{}

// NewReconciler creates a new reconciler.
func NewReconciler(cfg ReconcilerConfig) *Reconciler {
	return &Reconciler{
		cfg: cfg,
	}
}

func (r *Reconciler) mustCreateNewObject(objectType client.Object) (client.Object, error) {
	if r.cfg.OnlyMetadata {
		partialObj := &metav1.PartialObjectMetadata{}
		partialObj.SetGroupVersionKind(objectType.GetObjectKind().GroupVersionKind())

		return partialObj, nil
	}

	t := reflect.TypeOf(objectType).Elem()
	obj, ok := reflect.New(t).Interface().(client.Object)
	if !ok {
		err := fmt.Errorf("failed to create a new object of type %T", objectType)
		r.cfg.Logger.LogAttrs(context.Background(), slog.LevelError,
			"failed to create a new object",
			logging.LogAttrCategory(logging.LogCategoryK8s),
			logging.LogAttrError(err))
		return nil, err
	}
	return obj, nil
}

// Reconcile implements the reconcile.Reconciler Reconcile method.
func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	if r.cfg.NamespacedNameFilter != nil {
		if shouldProcess, msg := r.cfg.NamespacedNameFilter(req.NamespacedName); !shouldProcess {
			r.cfg.Logger.Info(msg)
			return reconcile.Result{}, nil
		}
	}

	obj, err := r.mustCreateNewObject(r.cfg.ObjectType)
	if err != nil {
		return reconcile.Result{}, err
	}

	gvk, err := apiutil.GVKForObject(obj, scheme)
	if err != nil {
		// this should not happen
		r.cfg.Logger.LogAttrs(
			context.Background(), slog.LevelError,
			fmt.Sprintf("could not extract GVK for object: %T", obj),
		)
	}

	r.cfg.Logger.LogAttrs(context.Background(), slog.LevelDebug,
		"Reconciling the resource",
		logging.LogAttrCategory(logging.LogCategoryK8s),
		logging.LogAttrKeyGVK(req.NamespacedName, gvk))

	if err := r.cfg.Getter.Get(ctx, req.NamespacedName, obj); err != nil {
		if !apierrors.IsNotFound(err) {
			r.cfg.Logger.LogAttrs(context.Background(), slog.LevelError,
				"Failed to get the resource",
				logging.LogAttrCategory(logging.LogCategoryK8s),
				logging.LogAttrKeyGVK(req.NamespacedName, gvk))

			return reconcile.Result{}, err
		}
		// The resource does not exist (was deleted).
		obj = nil
	}

	var e any
	var op string

	if obj == nil {
		e = &events.DeleteEvent{
			Type:           r.cfg.ObjectType,
			NamespacedName: req.NamespacedName,
		}
		op = "Deleted"
	} else {
		e = &events.UpsertEvent{
			Resource: obj,
		}
		op = "Upserted"
	}

	select {
	case <-ctx.Done():
		r.cfg.Logger.LogAttrs(context.Background(), slog.LevelInfo,
			"Did not process the resource because the context was canceled",
			logging.LogAttrKeyGVK(req.NamespacedName, gvk))
		return reconcile.Result{}, nil
	case r.cfg.EventCh <- e:
	}

	r.cfg.Logger.LogAttrs(
		context.Background(), slog.LevelDebug,
		fmt.Sprintf("%s the resource", op),
		logging.LogAttrCategory(logging.LogCategoryK8s),
		logging.LogAttrKeyGVK(req.NamespacedName, gvk),
	)

	return reconcile.Result{}, nil
}
