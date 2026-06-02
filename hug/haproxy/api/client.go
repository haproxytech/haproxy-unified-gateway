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
	"crypto/md5"
	"encoding/hex"
	"log/slog"

	clientnative "github.com/haproxytech/client-native/v6"
	parser "github.com/haproxytech/client-native/v6/config-parser"
	"github.com/haproxytech/client-native/v6/configuration"
	cfgoptions "github.com/haproxytech/client-native/v6/configuration/options"
	"github.com/haproxytech/client-native/v6/models"
	"github.com/haproxytech/client-native/v6/options"
	"github.com/haproxytech/client-native/v6/runtime"
	runtimeoptions "github.com/haproxytech/client-native/v6/runtime/options"
	defaultcrs "github.com/haproxytech/haproxy-unified-gateway/hug/haproxy/default_cr"
	"github.com/haproxytech/haproxy-unified-gateway/hug/haproxy/mandatory"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/logging"
)

type HAProxyClient interface { //nolint:interfacebloat
	APIStartTransaction() error
	APICommitTransaction() error
	APIFinalCommitTransaction() error
	APIDisposeTransaction()
	Frontend
	Bind
	Backend
	Defaults
	Global
	RuntimeClient() runtime.Runtime
}

type Frontend interface {
	FrontendCreate(frontend models.Frontend) error
	FrontendDelete(frontendName string) error
	FrontendsGet() (models.Frontends, error)
	FrontendGet(frontendName string) (models.Frontend, error)
	FrontendEdit(frontend models.Frontend) error
}

type Bind interface {
	BindsGet(parentType parser.Section, name string) (models.Binds, error)
	BindCreate(parentType parser.Section, name string, bind models.Bind) error
	BindEdit(parentType parser.Section, name string, bind models.Bind) error
	BindDelete(parentType parser.Section, name string, bind string) error
	BindDeleteAll(parentType parser.Section, name string) error
}

type Backend interface {
	BackendCreate(frontend models.Backend) error
	BackendDelete(backendName string) error
	BackendsGet() (models.Backends, error)
	BackendGet(backendName string) (models.Backend, error)
	BackendEdit(backend models.Backend) error
}

type Global interface {
	GlobalGet() (models.Global, error)
	GlobalEdit(global *models.Global, mergeStrategy string) error
}

type Defaults interface {
	DefaultsSectionGet(name string) (*models.Defaults, error)
	DefaultsSectionEdit(defaults *models.Defaults, mergeStrategy string) error
}

type Server interface {
	ServersGet(parentType parser.Section, name string) (models.Servers, error)
	ServerCreate(parentType parser.Section, name string, server models.Server) error
	ServerEdit(parentType parser.Section, name string, server models.Server) error
	ServerDelete(parentType parser.Section, name string, server string) error
	ServerDeleteAll(parentType parser.Section, name string) error
}

type clientNative struct {
	nativeAPI                           clientnative.HAProxyClient
	logger                              *slog.Logger
	activeTransaction                   string
	configurationHashAtTransactionStart string
	defaultGlobal                       models.Global
	mandatoryGlobal                     models.Global
	defaultDefaults                     models.Defaults
}

func New(logger *slog.Logger, transactionDir, configFile, programPath, runtimeSocket, pidFile string) (client HAProxyClient, err error) { //nolint:ireturn
	var runtimeClient runtime.Runtime
	if runtimeSocket != "" {
		runtimeClient, err = runtime.New(context.Background(), runtimeoptions.Socket(runtimeSocket), runtimeoptions.DoNotCheckRuntimeOnInit)
	} else {
		runtimeClient, err = runtime.New(context.Background())
	}
	if err != nil {
		return nil, err
	}

	confClient, err := configuration.New(
		context.Background(),
		cfgoptions.ConfigurationFile(configFile),
		cfgoptions.HAProxyBin(programPath),
		cfgoptions.UseModelsValidation,
		cfgoptions.UseMd5Hash,
		cfgoptions.TransactionsDir(transactionDir),
	)
	if err != nil {
		return nil, err
	}

	opt := []options.Option{
		options.Configuration(confClient),
		options.Runtime(runtimeClient),
	}
	cnHAProxyClient, err := clientnative.New(context.Background(), opt...)
	if err != nil {
		return nil, err
	}

	defaultGlobal, err := defaultcrs.DefaultGlobal(runtimeSocket, pidFile)
	if err != nil {
		return nil, err
	}

	mandatoryGlobal, err := mandatory.MandatoryGlobal(runtimeSocket, pidFile)
	if err != nil {
		return nil, err
	}

	defaultDefaults, err := defaultcrs.DefaultDefaults()
	if err != nil {
		return nil, err
	}

	cn := clientNative{
		nativeAPI:       cnHAProxyClient,
		logger:          logger,
		defaultGlobal:   defaultGlobal,
		mandatoryGlobal: mandatoryGlobal,
		defaultDefaults: defaultDefaults,
	}
	return &cn, nil
}

func (c *clientNative) APIStartTransaction() error {
	config, err := c.nativeAPI.Configuration()
	if err != nil {
		return err
	}
	version, errVersion := config.GetVersion("")
	if errVersion != nil || version < 1 {
		// silently fallback to 1
		version = 1
	}
	transaction, err := config.StartTransaction(version)
	if err != nil {
		return err
	}
	c.activeTransaction = transaction.ID

	hash, err := c.computeConfigurationHash(config)
	if err != nil {
		return err
	}
	c.configurationHashAtTransactionStart = hash

	return nil
}

func (c *clientNative) computeConfigurationHash(config configuration.Configuration) (string, error) {
	p, err := config.GetParser(c.activeTransaction)
	if err != nil {
		return "", err
	}
	// Note that p.String() does not include the hash!!!
	content := p.String()
	hash := md5.Sum([]byte(content))
	return hex.EncodeToString(hash[:]), err
}

func (c *clientNative) APICommitTransaction() error {
	config, err := c.nativeAPI.Configuration()
	if err != nil {
		return err
	}

	hash, err := c.computeConfigurationHash(config)
	if err != nil {
		return err
	}

	if c.configurationHashAtTransactionStart == hash {
		return config.DeleteTransaction(c.activeTransaction)
	}
	_, err = config.CommitTransaction(c.activeTransaction)
	return err
}

func (c *clientNative) APIFinalCommitTransaction() error {
	config, err := c.nativeAPI.Configuration()
	if err != nil {
		return err
	}

	hash, err := c.computeConfigurationHash(config)
	if err != nil {
		return err
	}

	if c.configurationHashAtTransactionStart == hash {
		if errDel := config.DeleteTransaction(c.activeTransaction); errDel != nil {
			c.logger.LogAttrs(
				context.Background(), slog.LevelError,
				"failed to delete transaction",
				logging.LogAttrError(errDel),
				slog.String("transactionID", c.activeTransaction),
			)
			return errDel
		}
		return nil
	}

	_, err = config.CommitTransaction(c.activeTransaction)
	if err != nil {
		c.logger.LogAttrs(
			context.Background(), slog.LevelError,
			"failed to commit transaction",
			logging.LogAttrError(err),
			slog.String("transactionID", c.activeTransaction),
		)
	}

	return err
}

func (c *clientNative) APIDisposeTransaction() {
	c.activeTransaction = ""
}

func (c *clientNative) RuntimeClient() runtime.Runtime {
	runtimeClient, _ := c.nativeAPI.Runtime()
	return runtimeClient
}
