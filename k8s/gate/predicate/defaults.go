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
package predicate

import (
	v3 "github.com/haproxytech/haproxy-unified-gateway/api/gate/v3"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/constants"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// DefaultsPredicate filters Defaults CR events to only those whose spec.name
// equals constants.DefaultsSectionName.
type DefaultsPredicate struct {
	predicate.Funcs
}

func (DefaultsPredicate) Create(e event.CreateEvent) bool {
	if e.Object == nil {
		return false
	}
	d, ok := e.Object.(*v3.Defaults)
	if !ok {
		return false
	}
	return d.Spec.Name == constants.DefaultsSectionName
}

func (DefaultsPredicate) Update(e event.UpdateEvent) bool {
	if e.ObjectOld != nil {
		if d, ok := e.ObjectOld.(*v3.Defaults); ok && d.Spec.Name == constants.DefaultsSectionName {
			return true
		}
	}
	if e.ObjectNew != nil {
		if d, ok := e.ObjectNew.(*v3.Defaults); ok && d.Spec.Name == constants.DefaultsSectionName {
			return true
		}
	}
	return false
}

func (DefaultsPredicate) Delete(e event.DeleteEvent) bool {
	if e.Object == nil {
		return false
	}
	d, ok := e.Object.(*v3.Defaults)
	if !ok {
		return false
	}
	return d.Spec.Name == constants.DefaultsSectionName
}
