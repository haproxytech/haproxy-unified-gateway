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

func (c *clientNative) ServersGet(parentType parser.Section, name string) (models.Servers, error) {
	configuration, err := c.nativeAPI.Configuration()
	if err != nil {
		return nil, err
	}
	_, servers, err := configuration.GetServers(string(parentType), name, c.activeTransaction)
	return servers, err
}

func (c *clientNative) ServerCreate(parentType parser.Section, name string, server models.Server) error {
	configuration, err := c.nativeAPI.Configuration()
	if err != nil {
		return err
	}
	return configuration.CreateServer(string(parentType), name, &server, c.activeTransaction, 0)
}

func (c *clientNative) ServerEdit(parentType parser.Section, name string, server models.Server) error {
	configuration, err := c.nativeAPI.Configuration()
	if err != nil {
		return err
	}
	return configuration.EditServer(server.Name, string(parentType), name, &server, c.activeTransaction, 0)
}

func (c *clientNative) ServerDelete(parentType parser.Section, name string, server string) error {
	configuration, err := c.nativeAPI.Configuration()
	if err != nil {
		return err
	}
	return configuration.DeleteServer(server, string(parentType), name, c.activeTransaction, 0)
}

func (c *clientNative) ServerDeleteAll(parentType parser.Section, name string) error {
	configuration, err := c.nativeAPI.Configuration()
	if err != nil {
		return err
	}
	_, servers, errGet := configuration.GetServers(string(parentType), name, c.activeTransaction)
	if errGet != nil {
		return errGet
	}

	for _, server := range servers {
		errDelete := configuration.DeleteServer(server.Name, string(parentType), name, c.activeTransaction, 0)
		if errDelete != nil {
			return errDelete
		}
	}
	return nil
}

func (c *clientNative) ServerReplaceAll(parentType parser.Section, name string, servers map[string]models.Server) error {
	err := c.ServerDeleteAll(parentType, name)
	if err != nil {
		return err
	}

	for _, server := range servers {
		if err := c.ServerCreate(parentType, name, server); err != nil {
			c.logger.LogAttrs(
				context.Background(), slog.LevelError, "failed to create server",
				logging.LogAttrError(err),
				slog.String("server", server.Name),
				slog.String("parent", name),
			)
			// Best effort
			continue
		}
	}

	return nil
}
