package process

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"time"

	"github.com/haproxytech/client-native/v6/runtime"
	"github.com/haproxytech/client-native/v6/runtime/options"
	hapi "github.com/haproxytech/haproxy-unified-gateway/hug/haproxy/api"
	"github.com/haproxytech/haproxy-unified-gateway/hug/haproxy/params"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/logging"
)

type goInitControl struct {
	API               hapi.HAProxyClient
	masterSocket      runtime.Runtime
	logger            *slog.Logger
	Params            params.Params
	masterSocketValid bool
}

// test seam: unit tests shrink this to avoid the 60s socket retry
var masterSocketRetryBudget = time.Minute

func newGoInitControl(api hapi.HAProxyClient, param params.Params, logger *slog.Logger) *goInitControl {
	sc := goInitControl{
		API:    api,
		Params: param,
		logger: logger,
	}

	masterSocket, err := runtime.New(context.Background(), options.MasterSocket(MASTER_SOCKET_PATH), options.AllowDelayedStart(masterSocketRetryBudget, time.Second))
	if err != nil {
		sc.logger.LogAttrs(context.Background(), slog.LevelError,
			"failed to initialize master socket",
			logging.LogAttrError(err))
		return &sc
	}
	sc.masterSocketValid = true
	sc.masterSocket = masterSocket

	return &sc
}

func (c *goInitControl) Service(action string) (string, error) {
	if c.Params.Test {
		c.logger.LogAttrs(context.Background(), slog.LevelInfo,
			"test mode: skipping HAProxy service action",
			slog.String("action", action))
		return "", nil
	}
	var cmd *exec.Cmd

	switch action { //revive:disable:identical-switch-branches
	case "start":
		// gopherd already started it
		return "", nil
	case "stop":
		// gopherd owns the stop
		return "", nil
	case "reload":
		if c.masterSocketValid {
			msg, err := c.masterSocket.Reload()
			if err == nil {
				c.logger.LogAttrs(context.Background(), slog.LevelDebug, msg)
				return msg, nil
			}
			c.logger.LogAttrs(context.Background(), slog.LevelError,
				"failed to reload",
				logging.LogAttrError(err))
		}

		cmd = exec.Command("/usr/local/sbin/gopherd", "haproxy", "restart")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return "", cmd.Run()
	default:
		return "", fmt.Errorf("unknown command '%s'", action)
	}
}

func (*goInitControl) UseAuxFile(_ bool) {
	// do nothing we always have it
}

func (c *goInitControl) SetAPI(api hapi.HAProxyClient) {
	c.API = api
}
