// Copyright 2025 HAProxy Technologies LLC
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
package api

import (
	"context"
	"log/slog"

	parser "github.com/haproxytech/client-native/v6/config-parser"
	"github.com/haproxytech/client-native/v6/models"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/logging"
)

func (c *clientNative) BindsGet(parentType parser.Section, name string) (models.Binds, error) {
	configuration, err := c.nativeAPI.Configuration()
	if err != nil {
		return nil, err
	}
	_, binds, err := configuration.GetBinds(string(parentType), name, c.activeTransaction)
	return binds, err
}

func (c *clientNative) BindCreate(parentType parser.Section, name string, bind models.Bind) error {
	configuration, err := c.nativeAPI.Configuration()
	if err != nil {
		return err
	}
	return configuration.CreateBind(string(parentType), name, &bind, c.activeTransaction, 0)
}

func (c *clientNative) BindEdit(parentType parser.Section, name string, bind models.Bind) error {
	configuration, err := c.nativeAPI.Configuration()
	if err != nil {
		return err
	}
	return configuration.EditBind(bind.Name, string(parentType), name, &bind, c.activeTransaction, 0)
}

func (c *clientNative) BindDelete(parentType parser.Section, name string, bind string) error {
	configuration, err := c.nativeAPI.Configuration()
	if err != nil {
		return err
	}
	return configuration.DeleteBind(bind, string(parentType), name, c.activeTransaction, 0)
}

func (c *clientNative) BindDeleteAll(parentType parser.Section, name string) error {
	configuration, err := c.nativeAPI.Configuration()
	if err != nil {
		return err
	}
	_, binds, errGet := configuration.GetBinds(string(parentType), name, c.activeTransaction)
	if errGet != nil {
		return errGet
	}

	for _, bind := range binds {
		errDelete := configuration.DeleteBind(bind.Name, string(parentType), name, c.activeTransaction, 0)
		if errDelete != nil {
			return errDelete
		}
	}
	return nil
}

func (c *clientNative) BindReplaceAll(parentType parser.Section, name string, binds map[string]models.Bind) error {
	err := c.BindDeleteAll(parentType, name)
	if err != nil {
		return err
	}

	for _, bind := range binds {
		if err := c.BindCreate(parentType, name, bind); err != nil {
			c.logger.LogAttrs(
				context.Background(), slog.LevelError, "failed to create bind",
				logging.LogAttrError(err),
				slog.String("bind", bind.Name),
				slog.String("parent", name),
			)
			// Best effort
			continue
		}
	}

	return nil
}
