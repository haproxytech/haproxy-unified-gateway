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
package api

import (
	"context"
	"log/slog"

	"github.com/haproxytech/client-native/v6/models"
	"github.com/haproxytech/haproxy-unified-gateway/hug/reload"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/logging"
)

func (c *clientNative) FrontendCreate(frontend models.Frontend) error {
	configuration, err := c.nativeAPI.Configuration()
	if err != nil {
		return err
	}
	errCreate := configuration.CreateStructuredFrontend(&frontend, c.activeTransaction, 0)
	if errCreate != nil {
		// ... maybe it's already existing, so just edit it.
		if err := configuration.EditStructuredFrontend(frontend.Name, &frontend, c.activeTransaction, 0); err != nil {
			c.logger.LogAttrs(context.Background(), slog.LevelError, "failed to edit frontend",
				logging.LogAttrError(err),
				slog.String("frontend", frontend.Name),
			)
			return err
		}
	}
	reload.Instance().SetReload("Frontend upserted %s", frontend.Name)

	return nil
}

func (c *clientNative) FrontendDelete(frontendName string) error {
	configuration, err := c.nativeAPI.Configuration()
	if err != nil {
		return err
	}
	reload.Instance().SetReload("Frontend deleted %s", frontendName)
	return configuration.DeleteFrontend(frontendName, c.activeTransaction, 0)
}

func (c *clientNative) FrontendsGet() (models.Frontends, error) {
	configuration, err := c.nativeAPI.Configuration()
	if err != nil {
		return nil, err
	}
	// TODO: complete with children
	_, frontends, err := configuration.GetStructuredFrontends(c.activeTransaction)

	return frontends, err
}

func (c *clientNative) FrontendGet(frontendName string) (models.Frontend, error) {
	configuration, err := c.nativeAPI.Configuration()
	if err != nil {
		return models.Frontend{}, err
	}
	_, frontend, err := configuration.GetStructuredFrontend(frontendName, c.activeTransaction)
	if err != nil {
		return models.Frontend{}, err
	}

	return *frontend, err
}

func (c *clientNative) FrontendEdit(frontend models.Frontend) error {
	configuration, err := c.nativeAPI.Configuration()
	if err != nil {
		return err
	}
	if err := configuration.EditFrontend(frontend.Name, &frontend, c.activeTransaction, 0); err != nil {
		c.logger.LogAttrs(context.Background(), slog.LevelError, "failed to edit frontend",
			logging.LogAttrError(err),
			slog.String("frontend", frontend.Name),
		)
		return err
	}

	reload.Instance().SetReload("Frontend upserted %s", frontend.Name)

	return nil
}
