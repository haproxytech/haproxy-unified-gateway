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
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/haproxytech/haproxy-unified-gateway/cmd/start"
	hugconfig "github.com/haproxytech/haproxy-unified-gateway/hug/configuration"
	haproxymgr "github.com/haproxytech/haproxy-unified-gateway/hug/haproxy"
	"github.com/haproxytech/haproxy-unified-gateway/hug/haproxy/api"
	haproxyparams "github.com/haproxytech/haproxy-unified-gateway/hug/haproxy/params"
	"github.com/haproxytech/haproxy-unified-gateway/hug/haproxy/process"
	"github.com/haproxytech/haproxy-unified-gateway/hug/jobs"
	"github.com/haproxytech/haproxy-unified-gateway/hug/version"
	controller "github.com/haproxytech/haproxy-unified-gateway/k8s/gate"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/haproxy/storage"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/logging"

	"github.com/joho/godotenv"
)

func main() {
	_ = godotenv.Load()
	_ = version.Set()
	ctx, _ := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGUSR1)

	// Controller HUGConfig from Flags
	hugConfig, err := hugconfig.Get()
	if err != nil {
		panic(err)
	}

	if hugConfig.JobCheckCRD {
		if err := jobs.CRDInstall(hugConfig.External.External); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println("CRD refresh job completed successfully")
		return
	}

	if hugConfig.JobGWAPI != "" {
		if err := jobs.GWAPIInstall(hugConfig.External.External, hugConfig.JobGWAPI); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println("Gateway API CRD installation completed successfully")
		return
	}

	fmt.Println(string(version.Logo))
	version.PrintVersion()

	// Setup Gate lib configuration from HUG binary configuration
	opts := start.SetupGateConfig(hugConfig)

	// Start controller
	cntlr, err := controller.New(opts)
	if err != nil {
		panic(err)
	}
	var wg sync.WaitGroup

	// Haproxy clients (runtime and configuration)
	gateconfig := cntlr.Configuration
	haproxyClient, err := api.New(gateconfig.Logger.With(logging.LogAttrCategory(logging.LogCategoryHaproxyCfgMgr)), gateconfig.HaproxyParams.CfgDir,
		gateconfig.HaproxyParams.MainCfgFile, gateconfig.HaproxyParams.HaproxyBinary, gateconfig.HaproxyParams.RuntimeSocket, gateconfig.HaproxyParams.PIDFile)
	if err != nil {
		err = fmt.Errorf("failed to initialize haproxy API client: %w", err)
		panic(err)
	}
	params := haproxyparams.Params{
		Test:             hugConfig.Test,
		UseWiths6Overlay: hugConfig.UseWiths6Overlay,
		UseWithPebble:    hugConfig.UseWithPebble,
		HaproxyDirs:      hugConfig.HaproxyDirs,
		ForceRestart:     hugConfig.ForceRestartHaproxyAtStartup,
	}
	p := process.New(params, haproxyClient, gateconfig.Logger)
	p.SetAPI(haproxyClient)
	mapsStorage := storage.NewMapsStorageEx(cntlr.Configuration.Logger,
		cntlr.Configuration.HaproxyParams.MapsDir)
	// ----------------
	// Start Haproxy App manager
	haproxyAppManager, err := haproxymgr.NewAppManager(ctx, &wg,
		gateconfig.TransferHaproxyConfChannel,
		// runtimeClientCh,
		haproxyClient, p,
		params,
		gateconfig.Logger,
		mapsStorage)
	if err != nil {
		panic(err)
	}
	cntlr.HaproxyClient = haproxyClient
	haproxyAppManager.Run()

	// ----------------
	// Start the controller
	go func() {
		err := cntlr.Run(ctx, &wg, mapsStorage)
		if err != nil {
			panic(err)
		}
	}()

	// --------------
	// Shutdown
	// --------------
	// refer to beginning of main
	// 	ctx, _ := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGUSR1)
	// ctx.Done() also called on signal received
	<-ctx.Done()
	cntlr.Configuration.Logger.Info("Context cancelled: shutting down controller")
	cntlr.Configuration.Logger.Info("Graceful shutdown requested...")

	// Stop controller logic if its still running
	haproxyAppManager.Stop()

	// Wait for background goroutines to finish
	wg.Wait()
	cntlr.Configuration.Logger.Info("Graceful shutdown complete. Exiting.")
}
